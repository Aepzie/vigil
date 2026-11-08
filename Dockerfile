# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/vigil ./cmd/vigil \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vigil /usr/local/bin/vigil
COPY --from=build --chown=65532:65532 /out/data /data
ENV VIGIL_DATA=/data
VOLUME /data
EXPOSE 2222
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/vigil"]
CMD ["serve", "--listen", ":2222"]
