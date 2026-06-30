FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ai-gateway .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/ai-gateway /ai-gateway
EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/ai-gateway"]
