#!/bin/bash
cd ~/GoLang/ChatApp/
go build -o bin/client src/client/main.go
go build -o bin/server src/server/main.go
ls -la bin/
