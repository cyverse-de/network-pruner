FROM golang:1.8

RUN go get github.com/jstemmer/go-junit-report

COPY . /go/src/github.com/cyverse-de/network-pruner
RUN go install github.com/cyverse-de/network-pruner

ENTRYPOINT ["network-pruner"]
CMD ["--help"]

ARG git_commit=unknown
ARG version="2.9.0"
ARG descriptive_version=unknown

LABEL org.cyverse.git-ref="$git_commit"
LABEL org.cyverse.version="$version"
LABEL org.cyverse.descriptive-version="$descriptive_version"
LABEL org.label-schema.vcs-ref="$git_commit"
LABEL org.label-schema.vcs-url="https://github.com/cyverse-de/network-pruner"
LABEL org.label-schema.version="$descriptive_version"
