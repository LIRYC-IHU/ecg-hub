package metrics

import (
	"context"
	"database/sql"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"
)

var (
	dbQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "db_query_duration_seconds",
		Help:    "Duration of DB queries by operation, table, and status.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"op", "table", "status"})

	dbPoolConns = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "db_pool_conns",
		Help: "Number of connections in the sql.DB pool by state.",
	}, []string{"state"})
)

func init() {
	Registry.MustRegister(dbQueryDuration, dbPoolConns)
}

// RegisterGORMCallbacks attaches before/after callbacks to each GORM operation type
// to time every query and record status (ok|error).
func RegisterGORMCallbacks(db *gorm.DB) {
	type startKey struct{}

	before := func(db *gorm.DB) {
		db.Statement.Context = context.WithValue(db.Statement.Context, startKey{}, time.Now())
	}

	// makeAfter closes over the operation name so the label is always correct,
	// regardless of which GORM callback chain fires.
	makeAfter := func(op string) func(*gorm.DB) {
		return func(db *gorm.DB) {
			start, ok := db.Statement.Context.Value(startKey{}).(time.Time)
			if !ok {
				return
			}
			status := "ok"
			if db.Error != nil && db.Error != gorm.ErrRecordNotFound {
				status = "error"
			}
			table := db.Statement.Table
			if table == "" {
				table = "unknown"
			}
			dbQueryDuration.WithLabelValues(op, table, status).Observe(time.Since(start).Seconds())
		}
	}

	// Each operation type must be registered on its own callback chain.
	db.Callback().Create().Before("gorm:create").Register("metrics:before_create", before)
	db.Callback().Create().After("gorm:create").Register("metrics:after_create", makeAfter("create"))

	db.Callback().Query().Before("gorm:query").Register("metrics:before_query", before)
	db.Callback().Query().After("gorm:query").Register("metrics:after_query", makeAfter("query"))

	db.Callback().Update().Before("gorm:update").Register("metrics:before_update", before)
	db.Callback().Update().After("gorm:update").Register("metrics:after_update", makeAfter("update"))

	db.Callback().Delete().Before("gorm:delete").Register("metrics:before_delete", before)
	db.Callback().Delete().After("gorm:delete").Register("metrics:after_delete", makeAfter("delete"))

	db.Callback().Row().Before("gorm:row").Register("metrics:before_row", before)
	db.Callback().Row().After("gorm:row").Register("metrics:after_row", makeAfter("row"))
}

// StartPoolExporter polls sql.DB.Stats() every interval and updates pool gauges.
// Call once after DB initialisation; runs in its own goroutine until ctx is cancelled.
func StartPoolExporter(ctx context.Context, sqlDB *sql.DB, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s := sqlDB.Stats()
				dbPoolConns.WithLabelValues("open").Set(float64(s.OpenConnections))
				dbPoolConns.WithLabelValues("in_use").Set(float64(s.InUse))
				dbPoolConns.WithLabelValues("idle").Set(float64(s.Idle))
			}
		}
	}()
}
