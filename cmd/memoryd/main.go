// Command memoryd runs the memory engine over HTTP, and sets up workspaces,
// services, issuers and grants for an operator.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"github.com/trailpad/memory/internal/embed"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/trailpad/memory/internal/access"
	httpapi "github.com/trailpad/memory/internal/api/http"
	"github.com/trailpad/memory/internal/capture"
	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres"
)

const usage = `memoryd runs the memory engine.

  memoryd serve
  memoryd migrate
  memoryd workspace -id ID
  memoryd service   -workspace ID -id ID -actions read,write,sync [-installations github/123@github.com,slack/T1]
  memoryd issuer    -workspace ID -issuer ISS -service ID -public-key FILE [-attests github]
  memoryd grant     -workspace ID -bucket ID -principal ID -rights read,write

Environment:
  MEMORY_DATABASE_URL  PostgreSQL connection string (required)
  MEMORY_ADDR          address to listen on (default :8080)
  MEMORY_AUDIENCE      name person tokens must be issued for (default trailpad-memory)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "memoryd:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, command string, args []string) error {
	url := os.Getenv("MEMORY_DATABASE_URL")
	if url == "" {
		return errors.New("MEMORY_DATABASE_URL is not set")
	}
	s, err := postgres.Open(ctx, url)
	if err != nil {
		return err
	}
	defer s.Close()

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	workspace := flags.String("workspace", "", "workspace id")
	id := flags.String("id", "", "id")
	actions := flags.String("actions", "", "comma-separated actions")
	installations := flags.String("installations", "", "comma-separated system/installation[@accounts]; accounts defaults to the installation")
	issuer := flags.String("issuer", "", "issuer name, as in the iss claim")
	service := flags.String("service", "", "service the issuer is registered for")
	publicKey := flags.String("public-key", "", "PEM file with the issuer's Ed25519 public key")
	attests := flags.String("attests", "", "comma-separated source systems the issuer proves accounts of")
	bucket := flags.String("bucket", "", "bucket id")
	principal := flags.String("principal", "", "principal id")
	rights := flags.String("rights", "", "comma-separated rights, empty to revoke")
	if err := flags.Parse(args); err != nil {
		return err
	}

	switch command {
	case "migrate":
		return s.Migrate(ctx)
	case "serve":
		if err := s.Migrate(ctx); err != nil {
			return err
		}
		return serve(ctx, s)
	case "workspace":
		return s.CreateWorkspace(ctx, store.Workspace{ID: *id})
	case "service":
		installed, err := parseInstallations(*installations)
		if err != nil {
			return err
		}
		key, hash := access.NewKey()
		svc := access.Service{Workspace: *workspace, ID: *id, Actions: list(*actions), Installations: installed}
		if err := s.CreateService(ctx, svc, hash); err != nil {
			return err
		}
		fmt.Println(key)
		return nil
	case "issuer":
		key, err := readPublicKey(*publicKey)
		if err != nil {
			return err
		}
		return s.AddIssuer(ctx, *workspace, access.Issuer{Issuer: *issuer, Service: *service, PublicKey: key, Attests: list(*attests)})
	case "grant":
		return s.Grant(ctx, *workspace, *bucket, *principal, list(*rights), "operator")
	}
	fmt.Fprint(os.Stderr, usage)
	return fmt.Errorf("unknown command %q", command)
}

func serve(ctx context.Context, s *postgres.Store) error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	profiles := profile.NewRegistry()
	audience := os.Getenv("MEMORY_AUDIENCE")
	if audience == "" {
		audience = "trailpad-memory"
	}
	addr := os.Getenv("MEMORY_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	semantic := &recall.Service{Store: s, Profiles: profiles}
	var backlog func(context.Context) (int, error)
	if url := os.Getenv("MEMORY_EMBEDDING_URL"); url != "" {
		dimensions, err := strconv.Atoi(os.Getenv("MEMORY_EMBEDDING_DIMENSIONS"))
		if err != nil || dimensions < 1 || dimensions > 2000 {
			return errors.New("MEMORY_EMBEDDING_DIMENSIONS must be between 1 and 2000")
		}
		model := os.Getenv("MEMORY_EMBEDDING_MODEL")
		if model == "" {
			return errors.New("MEMORY_EMBEDDING_MODEL is required when embeddings are enabled")
		}
		provider := &embed.HTTP{URL: url, Key: os.Getenv("MEMORY_EMBEDDING_KEY"), ModelName: model, Size: dimensions}
		var index store.VectorIndex
		backend := os.Getenv("MEMORY_VECTOR_STORE")
		switch backend {
		case "", "postgres":
			index, err = s.NewVectorIndex(ctx, dimensions)
		case "memory":
			index = store.NewMemoryVectorIndex()
		default:
			return errors.New("MEMORY_VECTOR_STORE must be postgres or memory")
		}
		if err != nil {
			return err
		}
		if err := s.PrepareEmbeddings(ctx, provider, backend == "memory"); err != nil {
			return err
		}
		semantic.Embedder = provider
		semantic.Vectors = index
		defaultFloor := 0.6
		semantic.MeaningFloor = &defaultFloor
		if raw := os.Getenv("MEMORY_EMBEDDING_FLOOR"); raw != "" {
			floor, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(floor) || floor < 0 || floor >= 1 {
				return errors.New("MEMORY_EMBEDDING_FLOOR must be at least 0 and below 1")
			}
			semantic.MeaningFloor = &floor
		}
		backlog = s.EmbeddingBacklog
		s.EmbeddingLog = log
		go s.RunEmbeddings(ctx, provider, index)
	}
	api := &httpapi.Server{
		EmbeddingBacklog: backlog,
		EmbeddingFailing: s.EmbeddingFailing,
		Auth:             &access.Authority{Directory: s, Audience: audience},
		Capture:          &capture.Capture{Store: s, Profiles: profiles},
		Recall:           semantic,
		Store:            s,
		Connectors:       s,
		Log:              log,
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr)
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("public key file is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key is not Ed25519")
	}
	return key, nil
}

func parseInstallations(s string) ([]access.Installation, error) {
	var out []access.Installation
	for _, item := range list(s) {
		where, accounts, _ := strings.Cut(item, "@")
		system, installation, ok := strings.Cut(where, "/")
		if !ok || system == "" || installation == "" {
			return nil, fmt.Errorf("installation %q is not system/installation[@accounts]", item)
		}
		if accounts == "" {
			accounts = installation
		}
		out = append(out, access.Installation{System: system, Installation: installation, Accounts: accounts})
	}
	return out, nil
}

func list(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
