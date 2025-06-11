FROM golang:1.24.2-alpine

ENV GOPROXY=https://proxy.golang.org,direct

# Install dependencies like ffmpeg
RUN apk update && apk add --no-cache ffmpeg git

WORKDIR /app

# ✅ Step 1: Copy only mod files first and cache dependencies
COPY go.mod .
COPY go.sum .
RUN go mod download

# ✅ Step 2: Copy full source *after* downloading dependencies
COPY . .

# ✅ Step 3: Build the Go binary
RUN go build -o content-service .

EXPOSE 8083

# ✅ Step 4: Setup startup script for DB wait
COPY wait-for-postgres.sh .
RUN chmod +x wait-for-postgres.sh

ENTRYPOINT ["./wait-for-postgres.sh"]
CMD ["./content-service"]