# tf-block-runner

This is a POC implementation of a Terraform runner written in Go. Config is
automatically read in from `application.yml` file placed next to the executable.
Config passed explicitly via program args will have precedence over file-config.
To see a full list of program arguments run

```Shell
go run main.go --help
```

## Run locally

Activate Go Modules. Consider the `go.mod` file for requirements.

Might need to go get dependencies before running:

```Shell
go get <dependencies>
```

To run the program use:

```Shell
go run main.go
```

To run with known_hosts support for ssh use:

```Shell
SSH_KNOWN_HOSTS=./resources/known_hosts go run main.go
```

## Run tests

```Shell
go test ./tfrun -v
go test ./crypto -v
```
