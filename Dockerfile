FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /memoryd ./cmd/memoryd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /memoryd /memoryd
EXPOSE 8080
ENTRYPOINT ["/memoryd"]
CMD ["serve"]
