FROM golang:1.14.9-alpine3.12 as blobber_build

RUN apk add --update --no-cache build-base linux-headers git cmake bash perl grep

ENV SRC_DIR=/blobber
ENV GO111MODULE=on

# Download the dependencies:
# Will be cached if we don't change mod/sum files
COPY ./code/go/0chain.net/core/go.mod           ./code/go/0chain.net/core/go.sum           $SRC_DIR/go/0chain.net/core/
COPY ./code/go/0chain.net/blobbercore/go.mod    ./code/go/0chain.net/blobbercore/go.sum    $SRC_DIR/go/0chain.net/blobbercore/
COPY ./code/go/0chain.net/blobber/go.mod        ./code/go/0chain.net/blobber/go.sum        $SRC_DIR/go/0chain.net/blobber/
COPY ./code/go/0chain.net/conductor/go.mod      ./code/go/0chain.net/conductor/go.sum      $SRC_DIR/go/0chain.net/conductor/

#Add the source code
ADD ./code/go/0chain.net $SRC_DIR/go/0chain.net

WORKDIR $SRC_DIR/go/0chain.net/blobber
RUN go mod download

ARG GIT_COMMIT
ENV GIT_COMMIT=$GIT_COMMIT
RUN go build -v -tags "bn256 development" -ldflags "-X 0chain.net/core/build.BuildTag=$GIT_COMMIT"

# Copy the build artifact into a minimal runtime image:
FROM golang:1.14.9-alpine3.12
RUN apk add gmp gmp-dev openssl-dev
ENV APP_DIR=/blobber
WORKDIR $APP_DIR
COPY --from=blobber_build $APP_DIR/go/0chain.net/blobber/blobber $APP_DIR/bin/blobber
