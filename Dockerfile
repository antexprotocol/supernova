FROM golang:1.24-alpine AS build

RUN apk add build-base git linux-headers

WORKDIR /work
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ENV GOFLAGS="-tags=bls12381"

RUN LEDGER_ENABLED=false CGO_ENABLED=1 go build -tags bls12381 -o supernova main.go sample_app.go

FROM alpine AS run

RUN apk add bash curl jq ca-certificates libc6-compat
RUN addgroup -S supernova && adduser -S supernova -G supernova
WORKDIR /home/supernova
COPY --from=build /work/supernova .
RUN mkdir -p .supernova/config .supernova/data
RUN chown -R supernova:supernova .

USER supernova

EXPOSE 26656 26657

ENTRYPOINT ["./supernova"] 