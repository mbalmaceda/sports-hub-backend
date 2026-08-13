ARG GO_VERSION=1.26.4
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -v -o /run-app ./cmd/api


FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /run-app /usr/local/bin/

# El binario es estático y no escribe en disco, así que no hay ninguna razón
# para que corra como root. `nobody` ya existe en la imagen base.
USER 65534:65534

CMD ["run-app"]
