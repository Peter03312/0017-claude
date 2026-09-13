# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go vet ./... && go test ./...
RUN CGO_ENABLED=0 go build -o /out/api . && CGO_ENABLED=0 go build -o /out/verify ./cmd/verify

FROM alpine:3.21
RUN adduser -S -D -H appuser
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/verify /usr/local/bin/verify
USER appuser
EXPOSE 8080
ENV GIN_MODE=release
CMD ["api"]
