FROM golang:1.26.0

WORKDIR /workspace

COPY go.mod ./
RUN go mod download

COPY . .

CMD ["go", "run", "./cmd/app"]
