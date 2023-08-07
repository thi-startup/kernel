FROM golang:1.19-buster AS build
WORKDIR /spitfire
COPY . .
RUN go build -o spitfire-build-kernel ./cmd/spitfire-build-kernel/main.go

FROM debian:buster
RUN apt-get update && apt-get install -y build-essential bc libssl-dev wget bison flex kmod
RUN echo 'spitfire:x:0:0:nobody:/:/bin/sh' >> /etc/passwd && \
    chown -R 0:0 /usr/src
USER spitfire
WORKDIR /usr/src
COPY --from=build /spitfire/spitfire-build-kernel /usr/bin/spitfire-build-kernel
ENTRYPOINT ["/usr/bin/spitfire-build-kernel", "-arch", "x86_64", "-build-kernel"]
