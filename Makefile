.PHONY: run build tidy test migrate-up migrate-down seed mirror-sync

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

tidy:
	go mod tidy

test:
	go test ./... -race

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

# Crea usuarios con una contraseña conocida y publicada en cmd/seed/main.go, así
# que exige ALLOW_SEED=true. El flag no se setea acá a propósito: tenés que
# escribirlo, y al escribirlo mirar a qué base apunta tu DATABASE_URL.
#
#   ALLOW_SEED=true make seed
seed:
	go run ./cmd/seed

# Reescribe el espejo de membresías en Firestore desde Postgres.
# Sin --apply solo dice cuántas tocaría.
mirror-sync:
	go run ./cmd/mirrorsync $(ARGS)
