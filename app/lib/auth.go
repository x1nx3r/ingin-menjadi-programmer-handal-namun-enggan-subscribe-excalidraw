package lib

import (
	"context"
	"log"
	"os"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

var FirebaseAuth *auth.Client

func InitAuth() {
	ctx := context.Background()
	opt := option.WithCredentialsFile(serviceAccountPath())

	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		log.Fatalf("firebase.NewApp: %v", err)
	}

	FirebaseAuth, err = app.Auth(ctx)
	if err != nil {
		log.Fatalf("app.Auth: %v", err)
	}

	log.Println("Firebase Admin SDK initialized")
}

func serviceAccountPath() string {
	if p := os.Getenv("FIREBASE_CREDENTIALS"); p != "" {
		return p
	}
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if matches := firebaseFileRe.FindString(e.Name()); matches != "" {
			return e.Name()
		}
	}
	return "service-account.json"
}
