FROM golang:1.26.0

WORKDIR /workspace

RUN go install github.com/air-verse/air@v1.64.5

COPY go.mod ./
RUN go mod download

COPY . .

EXPOSE 8080

CMD ["air", "-c", ".air.toml"]
