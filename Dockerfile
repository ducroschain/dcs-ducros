# Support setting various labels on the final image
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

# Build Geth in a stock Go builder container
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev linux-headers git cmake make g++

# Build and install RandomX library (required for RandomX consensus)
RUN git clone https://github.com/tevador/RandomX.git /tmp/RandomX && \
    cd /tmp/RandomX && \
    mkdir build && cd build && \
    cmake -DARCH=native -DBUILD_SHARED_LIBS=ON .. && \
    make -j$(nproc) && \
    make install && \
    ldconfig /usr/local/lib || true && \
    rm -rf /tmp/RandomX

# Get dependencies - will also be cached if we won't change go.mod/go.sum
COPY go.mod /go-ethereum/
COPY go.sum /go-ethereum/
RUN cd /go-ethereum && go mod download

ADD . /go-ethereum
RUN cd /go-ethereum && CGO_ENABLED=1 go run build/ci.go install ./cmd/geth

# Pull Geth into a second stage deploy alpine container
FROM alpine:latest

RUN apk add --no-cache ca-certificates libstdc++

# Copy RandomX library from builder
COPY --from=builder /usr/local/lib/librandomx.* /usr/local/lib/
RUN ldconfig /usr/local/lib || true

# Copy Geth binary
COPY --from=builder /go-ethereum/build/bin/geth /usr/local/bin/

EXPOSE 8545 8546 30303 30303/udp
ENTRYPOINT ["geth"]

# Add some metadata labels to help programmatic image consumption
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

LABEL commit="$COMMIT" version="$VERSION" buildnum="$BUILDNUM"
