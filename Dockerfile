# ---- build stage ----
FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY app ./app
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/infinity ./app

# ---- run stage ----
FROM alpine:3.22

# Remote TLS (Neon/Upstash) needs a CA bundle; alpine doesn't ship one.
RUN apk add --no-cache ca-certificates && adduser -D -H appuser

WORKDIR /app

COPY --from=build /out/infinity ./infinity
COPY --from=build /src/app/frontend ./app/frontend

USER appuser

ENV PORT=8000
EXPOSE 8000

# Render/Railway/Fly inject $PORT; default to 8000 locally.
# The single Go process is lightweight enough for a 512MB free tier.
CMD ["./infinity"]
