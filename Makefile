.PHONY: clean test

mqbridge: go.* *.go
	go build -o $@ ./cmd/mqbridge

clean:
	rm -rf mqbridge dist/

test:
	go test -v ./...

install:
	go install github.com/fujiwara/mqbridge/cmd/mqbridge

dist:
	goreleaser build --snapshot --clean
