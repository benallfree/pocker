lint:
	golangci-lint run -c ./golangci.yml ./...

release:
	goreleaser release --skip=publish --clean

test:
	go test ./... -v --cover

jstypes:
	go run ./plugins/jsvm/internal/types/types.go

test-report:
	go test ./... -v --cover -coverprofile=coverage.out
	go tool cover -html=coverage.out
