# `sourcesli.st`

An alternative way to browse Debian packages and bug reports, and find the right Debian ISO to download.

## Architecture

`sourcesli.st` is built to be as self-sufficient as possible so as not to hit Debian servers excessively. That is why it does not make use of [Debian's API](https://sources.debian.org/doc/api) but instead uses its own cache of Debian packages. This enables it to provide features such as autocompletion.

The architecture is basic:

- A TypeScript/React application in [`src/frontend`](src/frontend)
- A Go API in [`src/backend`](src/backend)
- A Postgres database in [`src/database`](src/database)
- Processing in Go in [`src/processing`](src/processing)

Processing contains a binary that can be run periodically to download and structure package data from Debian sources, and to insert it into the Postgres database. The Go API is its main client, and the React application is the view.

## Development

This project uses Docker and Docker Compose to manage the development environment. To start all the services, run:

```sh
docker compose up --build
```

This will build the Docker images for each service and start them.

The services are:

- `frontend`: A web server serving the frontend application, available at `http://localhost:3000`.
- `backend`: A Go API, available at `http://localhost:8080`.
- `database`: A PostgreSQL database.
- `processing`: A Go application for processing data.

### System requirements

- Docker
- Docker Compose
