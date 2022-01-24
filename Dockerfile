# Build the manager binary
FROM golang:1.16 as builder

WORKDIR /workspace
COPY . .
# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go

FROM quay.io/centos/centos:stream8
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]

ARG QUAY_TAG_EXPIRATION
LABEL "quay.expires-after"=${QUAY_TAG_EXPIRATION}
