##
# Compile application
##
FROM docker.io/library/golang:alpine AS build-env
ARG VERSION="dirty"

WORKDIR /app
COPY . .
# ge dependencies
RUN go mod tidy
# build binary
RUN CGO_ENABLED=0 go build -ldflags="-X codeberg.org/FreifunkBremen/yanic/cmd.VERSION=$VERSION -w -s" -o yanic


##
# Build Image
##
FROM scratch
COPY --from=build-env ["/etc/ssl/cert.pem", "/etc/ssl/certs/ca-certificates.crt"]
COPY --from=build-env /app/yanic /yanic
WORKDIR /
ENTRYPOINT [ "/yanic", "serve" ]
