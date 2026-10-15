FROM golang:1.22-alpine
WORKDIR /src
COPY . .
RUN go build -o /api .
EXPOSE 8080
CMD ["/api"]
