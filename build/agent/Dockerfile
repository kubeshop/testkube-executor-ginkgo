# syntax=docker/dockerfile:1
FROM golang:1.18-alpine
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0 
ENV GOOS=linux

RUN cd cmd/agent;go build -o /runner -mod mod -a .
RUN go install -mod=mod github.com/onsi/ginkgo/v2/ginkgo
RUN cp `which ginkgo` ginkgo

FROM golang:1.18-alpine
RUN apk --no-cache add ca-certificates git
RUN apk add --update bash curl
WORKDIR /root/
COPY --from=0 /runner /bin/runner
COPY --from=0 /build/ginkgo /bin/ginkgo
RUN go version
RUN ginkgo version
ENTRYPOINT ["/bin/runner"]
