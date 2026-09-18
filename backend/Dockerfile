FROM golang:1.26-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ .
COPY migrations/ /src/migrations/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/api /out/worker /out/healthcheck /out/migrate /app/
COPY --from=build /src/migrations /app/migrations
USER nonroot:nonroot
