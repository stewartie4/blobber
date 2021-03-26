.PHONY: test lint

test:
	git clone https://github.com/0chain/gosdk.git code/go/0chain.net/gosdk/ || echo gosdk already cloned.
	cd code/go/0chain.net/gosdk/ && git checkout jssdk && cd -
	@for mod_file in $$(find * -name go.mod); do mod_dir=$$(dirname $$mod_file); (cd $$mod_dir; go test ./...); done

lint:
	git clone https://github.com/0chain/gosdk.git code/go/0chain.net/gosdk/ || echo gosdk already cloned.
	cd code/go/0chain.net/gosdk/ && git checkout jssdk && cd -
	@for mod_file in $$(find * -name go.mod); do mod_dir=$$(dirname $$mod_file); (cd $$mod_dir; go mod download; golangci-lint run); done
