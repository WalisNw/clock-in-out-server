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

const(
	DateLayout = "2006/01/02(Mon)"
	TimeLayout = "15:04:05"
	NoRecord = "未打卡"
	Holiday = "休假日"
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
	var sql string
	m := make(map[time.Time]*record, 0)
	today := time.Now().Truncate(24*time.Hour)
	switch req.Type {
	case pb.QueryType_DAY:
		m[today] = &record{date: today}
		sql = `SELECT id, type, created_at FROM record WHERE member_id = $1 AND status = 1 AND created_at::date = date_trunc('day', now())  ORDER BY created_at`
	case pb.QueryType_LAST_SEVEN:
		for i:=1; i<=7;i++ {
			d := today.AddDate(0, 0, -i)
			m[d] = &record{date: d}
		}
		sql = `SELECT id, type, created_at FROM record WHERE member_id = $1 AND status = 1 AND created_at::date BETWEEN date_trunc('day', now() - interval '7 day') AND date_trunc('day', now() - interval '1 day') ORDER BY created_at`
	}
	rows, err := s.pool.Query(ctx, sql, req.Member.Id)
	if err != nil {
		log.Printf("failed to query. err: %v", err)
		return
	}
	records := make(records, 0)
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
			r = &record{date: k}
			m[k] = r
		}
		if clockType == pb.ClockType_CLOCK_IN && r.in == nil {
			r.in = &createdAt
		} else if clockType == pb.ClockType_CLOCK_OUT {
			r.out = &createdAt
		}
	}
	for _, r := range m {
		records = append(records, r)
	}
	sort.Sort(records)
	res.Records = make([]*pb.Record, 0)
	var zone *time.Location
	if z, err := time.LoadLocation("Asia/Taipei"); err == nil {
		zone = z
	}
	for _, r := range records {
		record := &pb.Record{Date: r.date.Format(DateLayout), In: NoRecord, Out: NoRecord}
		if w := r.date.Weekday(); w == time.Saturday || w==time.Sunday {
			record.In = Holiday
			record.Out = Holiday
		}
		if r.in != nil {
			record.In = r.in.In(zone).Format(TimeLayout)
		}
		if r.out != nil {
			record.Out = r.out.In(zone).Format(TimeLayout)
		}
		res.Records = append(res.Records, record)
	}
	return
}

type record struct {
	date time.Time
	in, out *time.Time
}

type records []*record

func (r records) Len() int {
	return len(r)
}

func (r records) Less(i, j int) bool {
	return r[i].date.Before(r[j].date)
}

func (r records) Swap(i, j int) {
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
