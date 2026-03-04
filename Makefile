BIN = bin
PKG = ./cmd/toss

.PHONY: run build build-all test clean vendor

# Default: run the development server
run:
	go run $(PKG)

# Build for current platform
build:
	@mkdir -p $(BIN)
	go build -o $(BIN)/toss $(PKG)

# Cross-compile for all platforms
build-all:
	@mkdir -p $(BIN)
	GOOS=darwin  GOARCH=arm64 go build -o $(BIN)/toss-darwin-arm64      $(PKG)
	GOOS=darwin  GOARCH=amd64 go build -o $(BIN)/toss-darwin-amd64      $(PKG)
	GOOS=windows GOARCH=amd64 go build -o $(BIN)/toss-windows-amd64.exe $(PKG)
	GOOS=linux   GOARCH=amd64 go build -o $(BIN)/toss-linux-amd64       $(PKG)
	@echo ""
	@ls -lh $(BIN)/toss-*

# Run tests
test:
	go test -v -count=1 $(PKG)

# Re-download vendored JS/CSS/fonts (only needed to update lib versions)
vendor:
	@cd cmd/toss/web/vendor && \
	curl -sfL -o highlight.min.js  "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js" && \
	curl -sfL -o github-dark.min.css "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github-dark.min.css" && \
	curl -sfL -o marked.min.js     "https://cdnjs.cloudflare.com/ajax/libs/marked/12.0.1/marked.min.js" && \
	curl -sfL -o purify.min.js     "https://cdnjs.cloudflare.com/ajax/libs/dompurify/3.0.9/purify.min.js" && \
	echo "JS/CSS updated. Fonts: edit font URLs in fonts.css manually if needed."

# Clean build artifacts
clean:
	rm -rf $(BIN)
