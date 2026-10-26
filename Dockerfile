FROM golang:1.26.9-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lms .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /lms /app/lms
ENV PORT=8080 DB_PATH=/data/lms_data.db UPLOAD_DIR=/data/uploads
EXPOSE 8080
ENTRYPOINT ["/app/lms"]
