# Build context is the repository root: docker build -f deploy/Dockerfile .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcpd ./cmd/mcpd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mcpd /usr/local/bin/mcpd
USER nonroot:nonroot
EXPOSE 8787
ENTRYPOINT ["/usr/local/bin/mcpd"]
# No -addr: the server binds $PORT when a PaaS injects it, else :8787 (EXPOSE).
CMD ["-transport", "http"]
