FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags='-s -w' -o /out/analytics-api .

FROM node:24-bookworm-slim
ENV FINGERPRINT_CATALOG_SYNC=1
# Bootstrap HTTPS on the slim image before installing its own CA package.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN useradd --system --create-home --uid 10001 analytics && mkdir -p /data && chown analytics:analytics /data \
 && sed -i 's|http://deb.debian.org/|https://deb.debian.org/|g' /etc/apt/sources.list.d/debian.sources \
 && apt-get -o Acquire::Retries=3 -o APT::Update::Error-Mode=any update \
 && apt-get -o Acquire::Retries=3 install -y --no-install-recommends ca-certificates wget \
 && rm -rf /var/lib/apt/lists/*
USER analytics
WORKDIR /app
COPY --from=build /out/analytics-api /app/analytics-api
COPY fingerprint /app/fingerprint
EXPOSE 8080
ENTRYPOINT ["/app/analytics-api"]
