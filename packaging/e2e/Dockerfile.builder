FROM debian:13
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git gnupg dpkg-dev debhelper \
    build-essential fakeroot apt-utils gzip openssl xz-utils \
    golang-go \
  && rm -rf /var/lib/apt/lists/*
