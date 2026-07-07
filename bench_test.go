package scan

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "github.com/stephenafamo/fakedb"
)

// Benchmark Command: go test -v -run=XXX -cpu 1,2,4 -benchmem -bench=. -memprofile mem.prof

var (
	db       *sql.DB
	dataSize = 100
	wideSize = 1000
)

func TestMain(m *testing.M) {
	var err error

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	db, err = sql.Open("test", "bench")
	if err != nil {
		panic(fmt.Errorf("Error opening testdb %w", err))
	}
	defer db.Close()

	err = prepareData(ctx)
	if err != nil {
		panic(err)
	}

	for _, w := range []struct {
		table   string
		numCols int
	}{
		{"wide5", 5},
		{"wide15", 15},
		{"wide45", 45},
	} {
		if err := prepareWideData(ctx, w.table, w.numCols); err != nil {
			panic(err)
		}
	}

	exitVal := m.Run()

	os.Exit(exitVal)
}

func BenchmarkScanAll(b *testing.B) {
	b.StopTimer()
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		rows, err := db.Query("SELECT|user||")
		if err != nil {
			panic(err)
		}
		b.StartTimer()
		if _, err := AllFromRows(ctx, StructMapper[Userss](), rows); err != nil {
			panic(err)
		}
		rows.Close()
	}
}

func BenchmarkScanOne(b *testing.B) {
	b.StopTimer()
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		rows, err := db.Query("SELECT|user||")
		if err != nil {
			panic(err)
		}
		b.StartTimer()
		if _, err := OneFromRows(ctx, StructMapper[Userss](), rows); err != nil {
			panic(err)
		}
		rows.Close()
	}
}

func BenchmarkScanWide5(b *testing.B)  { benchmarkScanWide[Wide5](b, "wide5") }
func BenchmarkScanWide15(b *testing.B) { benchmarkScanWide[Wide15](b, "wide15") }
func BenchmarkScanWide45(b *testing.B) { benchmarkScanWide[Wide45](b, "wide45") }

func benchmarkScanWide[T any](b *testing.B, table string) {
	b.StopTimer()
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		rows, err := db.Query("SELECT|" + table + "||")
		if err != nil {
			panic(err)
		}
		b.StartTimer()
		if _, err := AllFromRows(ctx, StructMapper[T](), rows); err != nil {
			panic(err)
		}
		rows.Close()
	}
}

func prepareData(ctx context.Context) error {
	create := "CREATE|user|id=int64,username=string,password=string"
	create += ",email=string,mobile_phone=string,company=string,avatar_url=string"
	create += ",role=int16,last_online_at=int64,create_at=datetime,update_at=datetime"

	if _, err := db.ExecContext(ctx, create); err != nil {
		return err
	}

	for i := 0; i < dataSize; i++ {
		userName := fmt.Sprintf("user%d", i+1)
		password := fmt.Sprintf("password%d", i+1)
		email := fmt.Sprintf("user%d@sqlscan.com", i+1)
		mobilePhone := fmt.Sprintf("%d", 10000*(i+1))
		company := fmt.Sprintf("company%d", i+1)
		avatarURL := fmt.Sprintf("http://sqlscan.com/avatar/%d", i+1)
		role := i % 3
		lastOnlineAt := time.Now().Unix() + int64(i)
		createAt := time.Now().UTC()
		updateAt := time.Now().UTC()
		_, err := db.Exec(`INSERT|user|id=?,username=?,password=?,email=?,mobile_phone=?,company=?,avatar_url=?,role=?,last_online_at=?,create_at=?,update_at=?`,
			i, userName, password, email, mobilePhone, company, avatarURL, role, lastOnlineAt, createAt, updateAt)
		if err != nil {
			return err
		}
	}

	return nil
}

func prepareWideData(ctx context.Context, table string, numCols int) error {
	colDefs := make([]string, numCols)
	colAssigns := make([]string, numCols)
	for i := range colDefs {
		colDefs[i] = fmt.Sprintf("col_%d=string", i)
		colAssigns[i] = fmt.Sprintf("col_%d=?", i)
	}

	create := fmt.Sprintf("CREATE|%s|%s", table, strings.Join(colDefs, ","))
	if _, err := db.ExecContext(ctx, create); err != nil {
		return err
	}

	insert := fmt.Sprintf("INSERT|%s|%s", table, strings.Join(colAssigns, ","))
	args := make([]any, numCols)
	for i := 0; i < wideSize; i++ {
		for c := range args {
			args[c] = fmt.Sprintf("value_%d_%d", i, c)
		}
		if _, err := db.ExecContext(ctx, insert, args...); err != nil {
			return err
		}
	}

	return nil
}

type Wide5 struct {
	Col0 string `db:"col_0"`
	Col1 string `db:"col_1"`
	Col2 string `db:"col_2"`
	Col3 string `db:"col_3"`
	Col4 string `db:"col_4"`
}

type Wide15 struct {
	Col0  string `db:"col_0"`
	Col1  string `db:"col_1"`
	Col2  string `db:"col_2"`
	Col3  string `db:"col_3"`
	Col4  string `db:"col_4"`
	Col5  string `db:"col_5"`
	Col6  string `db:"col_6"`
	Col7  string `db:"col_7"`
	Col8  string `db:"col_8"`
	Col9  string `db:"col_9"`
	Col10 string `db:"col_10"`
	Col11 string `db:"col_11"`
	Col12 string `db:"col_12"`
	Col13 string `db:"col_13"`
	Col14 string `db:"col_14"`
}

type Wide45 struct {
	Col0  string `db:"col_0"`
	Col1  string `db:"col_1"`
	Col2  string `db:"col_2"`
	Col3  string `db:"col_3"`
	Col4  string `db:"col_4"`
	Col5  string `db:"col_5"`
	Col6  string `db:"col_6"`
	Col7  string `db:"col_7"`
	Col8  string `db:"col_8"`
	Col9  string `db:"col_9"`
	Col10 string `db:"col_10"`
	Col11 string `db:"col_11"`
	Col12 string `db:"col_12"`
	Col13 string `db:"col_13"`
	Col14 string `db:"col_14"`
	Col15 string `db:"col_15"`
	Col16 string `db:"col_16"`
	Col17 string `db:"col_17"`
	Col18 string `db:"col_18"`
	Col19 string `db:"col_19"`
	Col20 string `db:"col_20"`
	Col21 string `db:"col_21"`
	Col22 string `db:"col_22"`
	Col23 string `db:"col_23"`
	Col24 string `db:"col_24"`
	Col25 string `db:"col_25"`
	Col26 string `db:"col_26"`
	Col27 string `db:"col_27"`
	Col28 string `db:"col_28"`
	Col29 string `db:"col_29"`
	Col30 string `db:"col_30"`
	Col31 string `db:"col_31"`
	Col32 string `db:"col_32"`
	Col33 string `db:"col_33"`
	Col34 string `db:"col_34"`
	Col35 string `db:"col_35"`
	Col36 string `db:"col_36"`
	Col37 string `db:"col_37"`
	Col38 string `db:"col_38"`
	Col39 string `db:"col_39"`
	Col40 string `db:"col_40"`
	Col41 string `db:"col_41"`
	Col42 string `db:"col_42"`
	Col43 string `db:"col_43"`
	Col44 string `db:"col_44"`
}

type Userss struct {
	ID           int       `db:"id"`
	UserName     string    `db:"username"`
	Password     string    `db:"password"`
	Email        string    `db:"email"`
	MobilePhone  string    `db:"mobile_phone"`
	Company      string    `db:"company"`
	AvatarURL    string    `db:"avatar_url"`
	Role         int       `db:"role"`
	LastOnlineAt int64     `db:"last_online_at"`
	CreateAt     time.Time `db:"create_at"`
	UpdateAt     time.Time `db:"update_at"`
}

func TestPrepare(t *testing.T) {
	cnt := 0
	rows, err := db.Query("SELECT|user||")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		cnt++
	}
	if cnt != dataSize {
		t.Error("wrong cnt")
	}
}
