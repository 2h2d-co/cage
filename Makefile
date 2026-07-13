.PHONY: test integration-test lint build clean completions man

test:
	go test ./...

integration-test:
	go test -v -count=1 -tags=integration ./integration

lint:
	golangci-lint run
	hk check --all --check

build:
	go build -o ./dist/cage .

completions: build
	mkdir -p ./dist/completions
	./dist/cage completion bash > ./dist/completions/cage.bash
	./dist/cage completion zsh > ./dist/completions/_cage
	./dist/cage completion fish > ./dist/completions/cage.fish

man: build
	mkdir -p ./dist/man
	./dist/cage man ./dist/man

clean:
	rm -rf ./dist
