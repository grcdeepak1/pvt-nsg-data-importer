###############################
# STEP 1: build binary #
###############################
FROM golang:1.23 AS builder

ARG CI_SERVER_HOST
ARG CI_JOB_TOKEN

WORKDIR /build
COPY . .

RUN go env -w "GOPRIVATE=${CI_SERVER_HOST}" && echo -e "machine ${CI_SERVER_HOST} login gitlab-ci-token password ${CI_JOB_TOKEN}" > ~/.netrc

RUN make cross

################################################
# STEP 2: build a minimal image and run binary #
################################################
FROM alpine

# Copy over the executable
COPY --from=builder /build/dist/nsg-data-importer /usr/local/go/bin/nsg-data-importer

WORKDIR /var/crusoe

RUN addgroup --gid 1000 crusoe
RUN adduser -D -g '' -G crusoe -u 1000 crusoe
USER crusoe

# Run the binary
ENTRYPOINT ["/usr/local/go/bin/nsg-data-importer"]
