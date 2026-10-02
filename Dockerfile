# The Argon API. Built on the EC2 host by deploy/30-install-api.sh (docker
# compose build api) and by CI. A static binary on a distroless base: no
# shell, no package manager, runs as a non-root user.
FROM golang:1.27-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY api ./api
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/argon-api ./cmd/api

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/argon-api /argon-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/argon-api"]
