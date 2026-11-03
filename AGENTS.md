# Working rules

* Never use the word "kind" in identifiers, wire fields, filenames, docs or comments. Say type, class or the precise term.
* New or changed behavior updates spec/SPEC.md and its examples.
* Run tests against Postgres with the race detector.
* Applied migrations are immutable.
* Add code comments only for non-obvious technical decisions.
