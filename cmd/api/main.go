package main

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/config"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/handler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), cfg.Database.URL)
	if err != nil {
		log.Fatalf("Unable to create connection pool : %v",err)	
	}
	defer pool.Close()
	// Fail fast if the DB isn't actually reachable.
	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("unable to reach database: %v", err)
	}

	queries := sqlc.New(pool)
        
	hotelHandler := handler.NewHotelHandler(queries)

    mux := http.NewServeMux()
	mux.HandleFunc("GET /health",func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("POST /hotels", hotelHandler.Create)
    
	addr := ":" + strconv.Itoa(cfg.API.Port)
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}