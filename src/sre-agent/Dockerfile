FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sre-agent .

FROM scratch

COPY --from=builder /out/sre-agent /sre-agent

ENTRYPOINT ["/sre-agent"]
