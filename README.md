# ASYNK - Asynchronous file watcher

## Motivation

Inspired by [Air](https://github.com/air-verse/air) and [reflex](https://github.com/cespare/reflex)

When building a web application with multiple code generation tasks such as open api document generation
and or database query generation such as [sqlc](https://github.com/sqlc-dev/sqlc), if you want them to
be generated with a separate file watcher you would have to use reflex to make that feasible.

Air on the other hand does not support running multiple asynchronous tasks, so you would have to
configure it in such a way that on any file change it would first run code generation and then after
that it would run the build command even though most of the time you are not actually modifying the
database migrations or files that generate your database access layer.

Asynk aims to solve this issue by allowing you to configure multiple tasks that can be executed
asynchronously and you can configure which tasks should be executed after which tasks with asynk's
dependency array.

## Features

- Asynk's own logs are as bland as possible not to distract you
- All of the logs from the tasks are propagated to the same console with a unique color for the task id
- Supports inclusion and exclusion of files with glob patterns.
- Supports running commands asynchronously by default
- You can decide which commands block the execution of other commands.
- Supports per-task working directories.
- Supports env files and per-task environment variables.

## Road to V1

- Watching new folders created after asynk was started
- Support env file loading and allow the user of asynk to specify passed env values for tasks.
- Other things missing?

## Installation

Currently you can install the application via `go install`
on the command line.

```sh
go install github.com/anton7r/asynk@latest
```

## Usage

To get started, first you should create `asynk.yaml` file,
you can look into the example yaml file for a more complex example.

Here's a simplified `asynk.yaml` configuration:
```yaml
# Currently the version does not
version: 0.0.1
# Shared contains - values used across the program and is shared by all the tasks
shared:
    # Log level is by default set to "info".
    # It controls how verbose asynk's own logs are.
    log-level: info
    # Values loaded from env files are available for interpolation and child processes.
    env-files: [.env]
    # Excludes directories globally for all the configurations
    # For example if you want to ignore node_modules folder.
    # Supports glob patterns
    exclude: node_modules

tasks:
    app-runner:
        # The task type can either be build or continuous
        # Here is it set to continuos to indicate that
        # the task should be always running
        type: continuous
        # Run the application built by the 'go-build' task
        run:
          command: ./bin/app
        # Watches for the binary changes
        include: bin/app
        # indicate that the completion of
        # go build is a dependency of the
        # 'app-runner' task.
        dependencies: [go-build]

    go-build:
        # The task type can either be build or continuous
        # Here is it set to to build to indicate
        # that it should only run once after a file change
        type: build
        # Prefer command + args for predictable arguments and quoting.
        run:
          command: go
          args: [build, -o, ./bin/app]
        # Watches for all go file changes under the project root
        include: **.go

```

### Task commands, working directories, and environment

`run` can be a single command object or a list of command objects. Commands in a
list run sequentially.

```yaml
tasks:
  frontend:
    type: continuous
    cwd: ../client
    env:
      - VITE_API_URL=${API_URL}
    run:
      command: pnpm
      args: [run, dev]

  generate:
    type: build
    run:
      - command: goose
        args: [up]
      - command: jet
        args:
          - -dsn=postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable
          - -path=./gen/jet/
```

`cwd` is resolved relative to the directory containing `asynk.yaml`.
`working-dir` is also accepted as an alias for `cwd`.

Child process environment values are merged in this order:

```txt
parent process env < shared env-files < per-task env
```

Legacy string commands remain supported:

```yaml
run: go build -o "./tmp bin/app.exe" .
```

For complex commands, prefer `command` + `args`. If shell features are required,
opt in explicitly:

```yaml
run:
  shell: true
  command: cd ../client && pnpm run dev
```

Once you have configured `asynk.yaml`, the next steps would be to run the following command inside of the same folder as `asynk.yaml`:

```sh
asynk
```

### Command Line Options

- `--once`: Run all tasks once and exit without watching for file changes. This is useful for CI/CD pipelines or one-time builds where you don't need the file watcher.

  ```sh
  asynk --once
  ```

## Managed ports and service exports

Continuous tasks can opt in to managed port assignment. Asynk checks whether the last assigned or preferred port is available and falls back to the configured range when a previous process or another application is still holding it. The assigned value is injected into the task environment and can be used in `run` commands with `${PORT}` interpolation.

Backends can also expose a stable local HTTP/WebSocket proxy. This lets a frontend use one stable URL while Asynk moves the backend process to a new port when needed.

```yaml
tasks:
  backend:
    type: continuous
    run: "go run ./cmd/api --port ${PORT}"
    include:
      - "**/*.go"
    port:
      preferred: 3000
      range:
        start: 3000
        end: 3099
      expose:
        name: backend
        proxy:
          enabled: true
          preferred: 8080
          range:
            start: 8080
            end: 8099

  frontend:
    type: continuous
    run: "npm run dev"
    include:
      - "src/**"
      - "package.json"
    consumes:
      - task: backend
        env:
          VITE_API_URL: proxy-url
```

Consumer tasks wait until provider exports are available. When a frontend consumes a backend directly with `port` or `url`, it restarts on backend port changes by default. When it consumes `proxy-url`, the proxy target updates in place and the frontend does not restart unless `on-change: restart` is configured.
