package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/WalisNw/clock-in-out-server/proto"
)

type server struct {
	pool *pgxpool.Pool
	pb.UnimplementedClockServiceServer
}

func (s *server) Clock(ctx context.Context, req *pb.ClockRequest) (*pb.ClockResponse, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("start transaction failed. err: %v", err)
		return &pb.ClockResponse{Result: "Failed. Please try again later."}, err
	}
	var t time.Time
	if err = tx.QueryRow(ctx, `INSERT INTO record (member_id, type) VALUES ($1, $2) RETURNING created_at`, req.Member.GetId(), req.GetType().Number()).Scan(&t); err != nil {
		log.Printf("scan failed. err: %v", err)
		_ = tx.Rollback(ctx)
		return &pb.ClockResponse{Result: "Failed. Please try again later."}, err
	}
	if err = tx.Commit(ctx); err != nil {
		log.Printf("commit error. err: %v", err)
		_ = tx.Rollback(ctx)
		return &pb.ClockResponse{Result: "Failed. Please try again later."}, err
	}
	typeMsg := "上班"
	if req.GetType().Number() == 1 {
		typeMsg = "下班"
	}
	return &pb.ClockResponse{Result: fmt.Sprintf("%s 打卡成功", typeMsg), Time: timestamppb.New(t)}, err
}

func main() {
	log.SetFlags(log.LstdFlags|log.Lshortfile)
	port := "8080"
	if _p := os.Getenv("PORT"); _p != "" {
		port = _p
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var opts []grpc.ServerOption

	pool, err := pgxpool.Connect(ctx, os.Getenv("DB_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	srv := grpc.NewServer(opts...)
	s := &server{
		pool: pool,
	}
	pb.RegisterClockServiceServer(srv, s)


	l, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		pool.Close()
		log.Fatal(err)
	}

	go func() {
		log.Printf("listening on %s", port)
		if err := srv.Serve(l); err != nil {
			log.Printf("server start failed. err : %v", err)
		}
		stop()
	}()

	<-ctx.Done()

	log.Println("shutting down")
	srv.GracefulStop()
}
