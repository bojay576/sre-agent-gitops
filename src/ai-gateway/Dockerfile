FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ai-gateway .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/ai-gateway /ai-gateway
EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/ai-gateway"]
