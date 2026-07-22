package lib

import (
	"context"
	"log"
	"os"
	"regexp"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

var FirebaseAuth *auth.Client

// firebaseFileRe matches the naming convention Google uses for downloaded
// service account JSON keys: <project>-firebase-adminsdk-<suffix>.json
var firebaseFileRe = regexp.MustCompile(`-firebase-adminsdk-.*\.json`)

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

// serviceAccountPath resolves the Firebase service account credentials in
// priority order:
//  1. FIREBASE_CREDENTIALS env var (production / CI)
//  2. Auto-discovery: any file in CWD matching the Firebase adminsdk naming convention
//  3. Static fallback "service-account.json"
func serviceAccountPath() string {
	if p := os.Getenv("FIREBASE_CREDENTIALS"); p != "" {
		return p
	}
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if firebaseFileRe.FindString(e.Name()) != "" {
			return e.Name()
		}
	}
	return "service-account.json"
}
