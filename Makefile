# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## tidy: format code and tidy modfile
.PHONY: tidy
tidy:
	go fmt ./...
	go mod tidy -v

## audit: run quality control checks
.PHONY: audit
audit:
	go mod verify
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@latest -checks=all,-ST1000,-U1000 ./...
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	go test -race -buildvcs -vet=off ./...


# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## test: run all tests
.PHONY: test
test:
	go test -v -race -buildvcs ./...

## test/cover: run all tests and display coverage
.PHONY: test/cover
test/cover:
	go test -v -race -buildvcs -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out

## generate_cert: generate_cert
.PHONY: generate_cert
generate_cert:
	go run generate_cert.go --host localhost

## build: build the application
.PHONY: build
build:
	@echo "Building project $(p)"
	go build -o=./tmp/bin/$(p) ./cmd/$(p)/main.go

## run: run the application with reloading on file changes
.PHONY: run
run:
	@echo "Running project $(p)"
	go run github.com/cosmtrek/air@v1.43.0 \
		--build.cmd "make build p=$(p)" --build.bin "./tmp/bin/$(p)" --build.delay "100" \
		--build.exclude_dir "" \
		--build.include_ext "go, tpl, templ, html, css, js, ts, sql, jpeg, jpg, gif, png, bmp, svg, webp, ico" \
		--build.exclude_regex "_templ.go"
		--misc.clean_on_exit "true"


# ==================================================================================== #
# OPERATIONS
# ==================================================================================== #

## push: push changes to the remote Git repository
.PHONY: push
push: tidy audit no-dirty
	git push
