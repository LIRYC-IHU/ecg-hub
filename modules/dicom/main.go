package main

import (
	"log"

	"github.com/LIRYC-IHU/ecg-hub-module-sdk"
)

func main() {
	handler := NewHandler()
	server := &shared.ModuleServer{
		Name:    "dicom",
		Port:    50051,
		Handler: handler,
	}
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
