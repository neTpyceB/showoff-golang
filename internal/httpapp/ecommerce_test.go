package httpapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMockPaymentProvider(t *testing.T) {
	p := mockPaymentProvider{}
	if _, err := p.Charge(context.Background(), 0, "pm_ok"); err == nil {
		t.Fatal("expected amount error")
	}
	failed, err := p.Charge(context.Background(), 100, "pm_fail")
	if err != nil || failed.Status != "failed" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	paid, err := p.Charge(context.Background(), 100, "pm_ok")
	if err != nil || paid.Status != "paid" {
		t.Fatalf("paid=%+v err=%v", paid, err)
	}
}

func TestEcommerceMemoryRepoAndHelpers(t *testing.T) {
	repo := newMemoryEcommerceRepository()
	now := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	p, err := repo.CreateProduct(context.Background(), createProductInput{Name: "A", PriceCents: 100, StockQty: 3}, now)
	if err != nil || p.ID != 1 {
		t.Fatalf("product=%+v err=%v", p, err)
	}
	items, _ := repo.ListProducts(context.Background())
	if len(items) != 1 {
		t.Fatalf("items=%+v", items)
	}
	_, _ = repo.CreateProduct(context.Background(), createProductInput{Name: "B", PriceCents: 50, StockQty: 2}, now)
	items, _ = repo.ListProducts(context.Background())
	if len(items) != 2 || items[0].ID > items[1].ID {
		t.Fatalf("sorted items=%+v", items)
	}

	o, err := repo.CreateOrder(context.Background(), 1, "k1", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: p.ID, Quantity: 1}},
		PaymentMethod: "pm_ok",
	}, mockPaymentProvider{}, now)
	if err != nil || o.Status != "paid" {
		t.Fatalf("order=%+v err=%v", o, err)
	}
	o2, err := repo.CreateOrder(context.Background(), 1, "k1", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: p.ID, Quantity: 1}},
		PaymentMethod: "pm_ok",
	}, mockPaymentProvider{}, now)
	if err != nil || o2.ID != o.ID {
		t.Fatalf("idem=%+v err=%v", o2, err)
	}

	if _, err := repo.CreateOrder(context.Background(), 1, "k2", createOrderInput{
		Items: []createOrderItemInput{{ProductID: 999, Quantity: 1}},
	}, mockPaymentProvider{}, now); !errors.Is(err, errProductNotFound) {
		t.Fatalf("err=%v", err)
	}
	if _, err := repo.CreateOrder(context.Background(), 1, "k3", createOrderInput{
		Items: []createOrderItemInput{{ProductID: p.ID, Quantity: 0}},
	}, mockPaymentProvider{}, now); !errors.Is(err, errInvalidOrder) {
		t.Fatalf("err=%v", err)
	}
	if _, err := repo.CreateOrder(context.Background(), 1, "k4", createOrderInput{
		Items: []createOrderItemInput{{ProductID: p.ID, Quantity: 999}},
	}, mockPaymentProvider{}, now); !errors.Is(err, errInsufficientStock) {
		t.Fatalf("err=%v", err)
	}
	if _, err := repo.CreateOrder(context.Background(), 1, "k5", createOrderInput{
		Items: []createOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	}, badPaymentProvider{}, now); err == nil {
		t.Fatal("expected payment provider error")
	}
	if _, err := repo.GetOrder(context.Background(), 999); !errors.Is(err, errOrderNotFound) {
		t.Fatalf("err=%v", err)
	}

	if got := userIDKey(42, " k "); got != "42|k" {
		t.Fatalf("key=%q", got)
	}
}

func TestEcommerceAPI(t *testing.T) {
	restoreGlobals(t)
	nowFn = func() time.Time { return time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC) }

	repo := newMemoryEcommerceRepository()
	api := newEcommerceAPI(repo, mockPaymentProvider{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /products", api.createProduct)
	mux.HandleFunc("GET /products", api.listProducts)
	mux.HandleFunc("POST /orders", api.createOrder)
	mux.HandleFunc("GET /orders/{id}", api.getOrder)

	t.Run("create product admin + list", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name":"Book","price_cents":1200,"stock_qty":5}`)
		req := httptest.NewRequest(http.MethodPost, "/products", body)
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "admin"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		req = httptest.NewRequest(http.MethodGet, "/products", nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("product validations/forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/products", bytes.NewBufferString(`{}`))
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/products", bytes.NewBufferString(`{bad`))
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "admin"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/products", bytes.NewBufferString(`{"name":"","price_cents":0,"stock_qty":-1}`))
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "admin"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("orders flow + idempotency + authz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items":[{"product_id":1,"quantity":2}],"payment_method":"pm_ok"}`))
		req.Header.Set("Idempotency-Key", "ord-1")
		req = req.WithContext(withAuthPrincipal(req.Context(), 10, "user"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}

		var first struct {
			Data order `json:"data"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&first)
		if first.Data.ID == 0 || first.Data.TotalCents <= 0 {
			t.Fatalf("order=%+v", first.Data)
		}

		req = httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items":[{"product_id":1,"quantity":2}],"payment_method":"pm_ok"}`))
		req.Header.Set("Idempotency-Key", "ord-1")
		req = req.WithContext(withAuthPrincipal(req.Context(), 10, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status=%d", rec.Code)
		}
		var second struct {
			Data order `json:"data"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&second)
		if second.Data.ID != first.Data.ID {
			t.Fatalf("idempotent ids differ: %d vs %d", first.Data.ID, second.Data.ID)
		}

		req = httptest.NewRequest(http.MethodGet, "/orders/1", nil)
		req = req.WithContext(withAuthPrincipal(req.Context(), 10, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}

		req = httptest.NewRequest(http.MethodGet, "/orders/1", nil)
		req = req.WithContext(withAuthPrincipal(req.Context(), 99, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/orders/bad", nil)
		req = req.WithContext(withAuthPrincipal(req.Context(), 10, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("order error branches", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/orders", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{}`))
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{bad`))
		req.Header.Set("Idempotency-Key", "x")
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items":[]}`))
		req.Header.Set("Idempotency-Key", "x2")
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items":[{"product_id":999,"quantity":1}],"payment_method":"pm_ok"}`))
		req.Header.Set("Idempotency-Key", "x3")
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("payment failed keeps response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items":[{"product_id":1,"quantity":1}],"payment_method":"pm_fail"}`))
		req.Header.Set("Idempotency-Key", "ord-fail")
		req = req.WithContext(withAuthPrincipal(req.Context(), 10, "user"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status=%d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "payment_failed") {
			t.Fatalf("body=%s", rec.Body.String())
		}
	})

	t.Run("repo internal errors map properly", func(t *testing.T) {
		bad := failingEcomRepo{err: errors.New("boom")}
		api := newEcommerceAPI(bad, badPaymentProvider{})
		mux := http.NewServeMux()
		mux.HandleFunc("POST /products", api.createProduct)
		mux.HandleFunc("GET /products", api.listProducts)
		mux.HandleFunc("POST /orders", api.createOrder)
		mux.HandleFunc("GET /orders/{id}", api.getOrder)

		req := httptest.NewRequest(http.MethodPost, "/products", bytes.NewBufferString(`{"name":"P","price_cents":1,"stock_qty":1}`))
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "admin"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/products", nil)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items":[{"product_id":1,"quantity":1}],"payment_method":"pm_ok"}`))
		req.Header.Set("Idempotency-Key", "x")
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/orders/1", nil)
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("createOrder error mapping branches", func(t *testing.T) {
		cases := []struct {
			name string
			err  error
			code int
		}{
			{name: "insufficient", err: errInsufficientStock, code: http.StatusConflict},
			{name: "invalid", err: errInvalidOrder, code: http.StatusBadRequest},
			{name: "idem", err: errIdempotencyKeyExists, code: http.StatusConflict},
		}
		for _, tc := range cases {
			repo := failingEcomRepo{err: tc.err}
			api := newEcommerceAPI(repo, mockPaymentProvider{})
			req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"items":[{"product_id":1,"quantity":1}]}`))
			req.Header.Set("Idempotency-Key", "k")
			req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
			rec := httptest.NewRecorder()
			api.createOrder(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("%s status=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
		}
	})

	t.Run("getOrder unauthorized and not found", func(t *testing.T) {
		api := newEcommerceAPI(newMemoryEcommerceRepository(), mockPaymentProvider{})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/orders/1", nil)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /orders/{id}", api.getOrder)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d", rec.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/orders/999", nil)
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d", rec.Code)
		}
	})
}

type failingEcomRepo struct{ err error }

func (f failingEcomRepo) CreateProduct(context.Context, createProductInput, time.Time) (product, error) {
	return product{}, f.err
}
func (f failingEcomRepo) ListProducts(context.Context) ([]product, error) { return nil, f.err }
func (f failingEcomRepo) CreateOrder(context.Context, int64, string, createOrderInput, paymentProvider, time.Time) (order, error) {
	return order{}, f.err
}
func (f failingEcomRepo) GetOrder(context.Context, int64) (order, error) { return order{}, f.err }

type badPaymentProvider struct{}

func (badPaymentProvider) Charge(context.Context, int64, string) (paymentResult, error) {
	return paymentResult{}, errors.New("pay error")
}
