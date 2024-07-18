# --- build stage ---
FROM golang:1.22-alpine AS build

WORKDIR /build
COPY . .

RUN apk add --no-cache make

RUN make

# --- make final container ---

FROM scratch
COPY --from=build /build/rssbundler /rssbundler

CMD ["/rssbundler"]
