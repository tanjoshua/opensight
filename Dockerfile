# syntax=docker/dockerfile:1

FROM node:22-bookworm-slim AS webbuild

WORKDIR /web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.26.5-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Replace the committed placeholder with the real SPA build before go:embed.
COPY --from=webbuild /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/opensight ./cmd/opensight

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/opensight /opensight

EXPOSE 8080

ENTRYPOINT ["/opensight"]
CMD ["serve"]
