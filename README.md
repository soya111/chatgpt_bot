## How to Build
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o handler ./main.go
```

## How to Zip and Deploy
```bash
build-lambda-zip.exe -output handler.zip handler
aws lambda update-function-code --function-name {{name}} --zip-file fileb://handler.zip 
```
