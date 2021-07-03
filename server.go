package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sort"
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

func (s *server) Clock(ctx context.Context, req *pb.ClockRequest) (res *pb.ClockResponse, err error) {
	tx, err := s.pool.Begin(ctx)
	res = &pb.ClockResponse{Result: "Failed. Please try again later."}
	if err != nil {
		log.Printf("start transaction failed. err: %v", err)
		return
	}
	var t time.Time
	if err = tx.QueryRow(ctx, `INSERT INTO record (member_id, type) VALUES ($1, $2) RETURNING created_at`, req.Member.GetId(), req.GetType().Number()).Scan(&t); err != nil {
		log.Printf("scan failed. err: %v", err)
		_ = tx.Rollback(ctx)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		log.Printf("commit error. err: %v", err)
		_ = tx.Rollback(ctx)
		return
	}
	typeMsg := "上班"
	if req.GetType().Number() == 1 {
		typeMsg = "下班"
	}
	return &pb.ClockResponse{Result: fmt.Sprintf("%s 打卡成功", typeMsg), Time: timestamppb.New(t)}, nil
}

func (s *server) Query(ctx context.Context, req *pb.QueryRequest) (res *pb.QueryResponse, err error) {
	res = &pb.QueryResponse{Member: req.Member, Type: req.Type}
	var field string
	switch req.Type {
	case pb.QueryType_DAY:
		field = "day"
	case pb.QueryType_WEEK:
		field = "week"
	case pb.QueryType_MONTH:
		field = "month"
	}
	rows, err := s.pool.Query(ctx, `SELECT id, type, created_at FROM record WHERE member_id = $1 AND status = 1 AND created_at::DATE >= DATE_TRUNC($2, NOW()) ORDER BY created_at`, req.Member.Id, field)
	if err != nil {
		log.Printf("failed to query. err: %v", err)
		return
	}
	m := make(map[time.Time]*pb.Record, 0)
	records := make(rs, 0)
	for rows.Next() {
		var (
			id int
			clockType pb.ClockType
			createdAt time.Time
		)
		if err := rows.Scan(&id, &clockType, &createdAt); err != nil {
			log.Printf("failed to scan. err: %v", err)
			return res, err
		}
		k := createdAt.Truncate(24*time.Hour)
		r, ok := m[k]
		if !ok {
			r = &pb.Record{Date: timestamppb.New(k)}
			m[k] = r
		}
		if clockType == pb.ClockType_CLOCK_IN && r.In == nil {
			r.In = timestamppb.New(createdAt)
		} else if clockType == pb.ClockType_CLOCK_OUT {
			r.Out = timestamppb.New(createdAt)
		}
	}
	for _, r := range m {
		records = append(records, r)
	}
	sort.Sort(records)
	res.Records = records
	return
}

type rs []*pb.Record

func (r rs) Len() int {
	return len(r)
}

func (r rs) Less(i, j int) bool {
	return r[i].Date.AsTime().Before(r[j].Date.AsTime())
}

func (r rs) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
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
