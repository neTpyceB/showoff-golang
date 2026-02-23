FROM golang:1.22

WORKDIR /workspace

COPY go.mod ./
RUN go mod download

COPY . .

CMD ["go", "run", "./cmd/hello"]
