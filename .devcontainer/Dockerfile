FROM golang:1.27

ARG TARGETARCH
# Keep in sync with sqlc-version in .github/workflows/test.yml (Dependabot cannot bump this ARG).
ARG SQLC_VERSION=1.31.1

RUN apt-get update \
    && apt-get install -y --no-install-recommends pkg-config libayatana-appindicator3-dev postgresql-client \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://downloads.sqlc.dev/sqlc_${SQLC_VERSION}_linux_${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin sqlc

# The official golang image sets GOTOOLCHAIN=local; let go.mod pick the Go version.
ENV GOTOOLCHAIN=auto
