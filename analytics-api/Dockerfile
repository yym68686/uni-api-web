FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=1
RUN go build -trimpath -ldflags='-s -w' -o /out/analytics-api .

FROM debian:bookworm-slim
RUN useradd --system --create-home --uid 10001 analytics && mkdir -p /data && chown analytics:analytics /data \
 && apt-get update && apt-get install -y --no-install-recommends ca-certificates wget \
 && rm -rf /var/lib/apt/lists/*
USER analytics
WORKDIR /app
COPY --from=build /out/analytics-api /app/analytics-api
EXPOSE 8080
ENTRYPOINT ["/app/analytics-api"]
