# --- build stage ---
FROM golang:1.22-alpine AS build

WORKDIR /build
COPY . .

RUN apk add --no-cache make ca-certificates

RUN make

# --- make final container ---

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /build/rssbundler /rssbundler

CMD ["/rssbundler"]
