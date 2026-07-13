FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sre-agent .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/sre-agent /sre-agent
USER nonroot:nonroot
ENTRYPOINT ["/sre-agent"]
