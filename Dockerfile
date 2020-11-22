FROM golang:1.15.0-alpine AS build
RUN apk add --update --no-cache git
WORKDIR /src
COPY ./go.* ./
RUN go mod download
COPY . .

ENV CGO_ENABLED 0
RUN go build -o /lssd -ldflags "-s -w"

FROM python:3-alpine
RUN apk add gcc musl-dev --no-cache \
	&& pip install streamlink \
	&& apk del gcc musl-dev --no-cache \
	&& rm -Rf /tmp/*

COPY --from=build /lssd /usr/local/bin/

VOLUME /records
ENV RECORD_DIR=/records

ENTRYPOINT ["lssd"]
