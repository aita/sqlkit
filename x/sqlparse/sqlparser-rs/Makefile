# Builds the Rust static library that the cgo binding links against.
# The Go side is gated behind the `sqlparser_rs` build tag; see doc.go.

RUST_DIR := rust
LIB := $(RUST_DIR)/target/release/libsqlkit_parserrs.a

.PHONY: all clean test

all: $(LIB)

$(LIB): $(RUST_DIR)/Cargo.toml $(wildcard $(RUST_DIR)/src/*.rs)
	cargo build --release --manifest-path $(RUST_DIR)/Cargo.toml

# Build the library and run the Go binding tests against it.
test: all
	go test -tags sqlparser_rs ./...

clean:
	cargo clean --manifest-path $(RUST_DIR)/Cargo.toml
