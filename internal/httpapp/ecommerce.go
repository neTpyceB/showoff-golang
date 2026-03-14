package httpapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

var (
	errProductNotFound      = errors.New("product not found")
	errOrderNotFound        = errors.New("order not found")
	errInsufficientStock    = errors.New("insufficient stock")
	errInvalidOrder         = errors.New("invalid order")
	errIdempotencyKeyExists = errors.New("idempotency key exists")
)

type product struct {
	ID         int64
	Name       string
	PriceCents int64
	StockQty   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type orderItem struct {
	ProductID      int64
	Quantity       int64
	UnitPriceCents int64
	LineTotalCents int64
}

type order struct {
	ID             int64
	UserID         int64
	Status         string
	TotalCents     int64
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Items          []orderItem
	PaymentStatus  string
	PaymentRef     string
}

type paymentResult struct {
	Status  string
	Ref     string
	Message string
}

type paymentProvider interface {
	Charge(context.Context, int64, string) (paymentResult, error)
}

type mockPaymentProvider struct{}

func (mockPaymentProvider) Charge(_ context.Context, amountCents int64, method string) (paymentResult, error) {
	if amountCents <= 0 {
		return paymentResult{}, errors.New("amount must be positive")
	}
	if strings.EqualFold(strings.TrimSpace(method), "pm_fail") {
		return paymentResult{Status: "failed", Ref: "mock-failed", Message: "payment declined"}, nil
	}
	return paymentResult{Status: "paid", Ref: "mock-paid", Message: "payment captured"}, nil
}

type createProductInput struct {
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	StockQty   int64  `json:"stock_qty"`
}

type createOrderItemInput struct {
	ProductID int64 `json:"product_id"`
	Quantity  int64 `json:"quantity"`
}

type createOrderInput struct {
	Items         []createOrderItemInput `json:"items"`
	PaymentMethod string                 `json:"payment_method"`
}

type ecommerceRepository interface {
	CreateProduct(context.Context, createProductInput, time.Time) (product, error)
	ListProducts(context.Context) ([]product, error)
	CreateOrder(context.Context, int64, string, createOrderInput, paymentProvider, time.Time) (order, error)
	GetOrder(context.Context, int64) (order, error)
}

type memoryEcommerceRepository struct {
	products      map[int64]product
	orders        map[int64]order
	idemByUserKey map[string]int64
	nextProductID int64
	nextOrderID   int64
}

func newMemoryEcommerceRepository() *memoryEcommerceRepository {
	return &memoryEcommerceRepository{
		products:      map[int64]product{},
		orders:        map[int64]order{},
		idemByUserKey: map[string]int64{},
		nextProductID: 1,
		nextOrderID:   1,
	}
}

func (r *memoryEcommerceRepository) CreateProduct(_ context.Context, in createProductInput, ts time.Time) (product, error) {
	p := product{
		ID:         r.nextProductID,
		Name:       in.Name,
		PriceCents: in.PriceCents,
		StockQty:   in.StockQty,
		CreatedAt:  ts,
		UpdatedAt:  ts,
	}
	r.products[p.ID] = p
	r.nextProductID++
	return p, nil
}

func (r *memoryEcommerceRepository) ListProducts(_ context.Context) ([]product, error) {
	out := make([]product, 0, len(r.products))
	for _, p := range r.products {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b product) int {
		return int(a.ID - b.ID)
	})
	return out, nil
}

func (r *memoryEcommerceRepository) CreateOrder(ctx context.Context, userID int64, idempotencyKey string, in createOrderInput, payments paymentProvider, ts time.Time) (order, error) {
	key := userIDKey(userID, idempotencyKey)
	if oid, ok := r.idemByUserKey[key]; ok {
		return r.orders[oid], nil
	}
	total := int64(0)
	items := make([]orderItem, 0, len(in.Items))
	for _, it := range in.Items {
		p, ok := r.products[it.ProductID]
		if !ok {
			return order{}, errProductNotFound
		}
		if it.Quantity <= 0 {
			return order{}, errInvalidOrder
		}
		if p.StockQty < it.Quantity {
			return order{}, errInsufficientStock
		}
		line := p.PriceCents * it.Quantity
		total += line
		items = append(items, orderItem{
			ProductID:      p.ID,
			Quantity:       it.Quantity,
			UnitPriceCents: p.PriceCents,
			LineTotalCents: line,
		})
	}
	pay, err := payments.Charge(ctx, total, in.PaymentMethod)
	if err != nil {
		return order{}, err
	}
	status := "paid"
	if pay.Status != "paid" {
		status = "payment_failed"
	}
	o := order{
		ID:             r.nextOrderID,
		UserID:         userID,
		Status:         status,
		TotalCents:     total,
		IdempotencyKey: idempotencyKey,
		CreatedAt:      ts,
		UpdatedAt:      ts,
		Items:          items,
		PaymentStatus:  pay.Status,
		PaymentRef:     pay.Ref,
	}
	if status == "paid" {
		for _, it := range in.Items {
			p := r.products[it.ProductID]
			p.StockQty -= it.Quantity
			p.UpdatedAt = ts
			r.products[it.ProductID] = p
		}
	}
	r.orders[o.ID] = o
	r.idemByUserKey[key] = o.ID
	r.nextOrderID++
	return o, nil
}

func (r *memoryEcommerceRepository) GetOrder(_ context.Context, id int64) (order, error) {
	o, ok := r.orders[id]
	if !ok {
		return order{}, errOrderNotFound
	}
	return o, nil
}

type ecommerceAPI struct {
	repo     ecommerceRepository
	payments paymentProvider
}

func newEcommerceAPI(repo ecommerceRepository, payments paymentProvider) *ecommerceAPI {
	return &ecommerceAPI{repo: repo, payments: payments}
}

func (a *ecommerceAPI) createProduct(w http.ResponseWriter, r *http.Request) {
	if role, _ := authRoleFromContext(r.Context()); role != "admin" {
		respondErrorJSON(w, r, http.StatusForbidden, apiError{Code: "forbidden", Message: "admin role required"})
		return
	}
	var in createProductInput
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "invalid_json", Message: "invalid JSON request body"})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.PriceCents <= 0 || in.StockQty < 0 {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "invalid product input"})
		return
	}
	p, err := a.repo.CreateProduct(r.Context(), in, nowFn().UTC())
	if err != nil {
		respondErrorJSON(w, r, http.StatusInternalServerError, apiError{Code: "internal_error", Message: "failed to create product"})
		return
	}
	respondJSON(w, r, http.StatusCreated, p)
}

func (a *ecommerceAPI) listProducts(w http.ResponseWriter, r *http.Request) {
	items, err := a.repo.ListProducts(r.Context())
	if err != nil {
		respondErrorJSON(w, r, http.StatusInternalServerError, apiError{Code: "internal_error", Message: "failed to list products"})
		return
	}
	respondJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func (a *ecommerceAPI) createOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := authUserIDFromContext(r.Context())
	if !ok {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "authentication required"})
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "Idempotency-Key header is required"})
		return
	}
	var in createOrderInput
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "invalid_json", Message: "invalid JSON request body"})
		return
	}
	if len(in.Items) == 0 {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "order must include at least one item"})
		return
	}
	o, err := a.repo.CreateOrder(r.Context(), userID, idempotencyKey, in, a.payments, nowFn().UTC())
	if err != nil {
		switch {
		case errors.Is(err, errProductNotFound):
			respondErrorJSON(w, r, http.StatusNotFound, apiError{Code: "product_not_found", Message: "product not found"})
		case errors.Is(err, errInsufficientStock):
			respondErrorJSON(w, r, http.StatusConflict, apiError{Code: "insufficient_stock", Message: "insufficient stock"})
		case errors.Is(err, errInvalidOrder):
			respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "validation_error", Message: "invalid order input"})
		case errors.Is(err, errIdempotencyKeyExists):
			respondErrorJSON(w, r, http.StatusConflict, apiError{Code: "idempotency_conflict", Message: "idempotency key conflict"})
		default:
			respondErrorJSON(w, r, http.StatusInternalServerError, apiError{Code: "internal_error", Message: "failed to create order"})
		}
		return
	}
	respondJSON(w, r, http.StatusCreated, o)
}

func (a *ecommerceAPI) getOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := authUserIDFromContext(r.Context())
	if !ok {
		respondErrorJSON(w, r, http.StatusUnauthorized, apiError{Code: "unauthorized", Message: "authentication required"})
		return
	}
	id, err := parseFileID(r.PathValue("id"))
	if err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{Code: "bad_request", Message: "invalid order id"})
		return
	}
	o, err := a.repo.GetOrder(r.Context(), id)
	if err != nil {
		if errors.Is(err, errOrderNotFound) {
			respondErrorJSON(w, r, http.StatusNotFound, apiError{Code: "not_found", Message: "order not found"})
			return
		}
		respondErrorJSON(w, r, http.StatusInternalServerError, apiError{Code: "internal_error", Message: "failed to fetch order"})
		return
	}
	role, _ := authRoleFromContext(r.Context())
	if role != "admin" && o.UserID != userID {
		respondErrorJSON(w, r, http.StatusForbidden, apiError{Code: "forbidden", Message: "you do not have access to this order"})
		return
	}
	respondJSON(w, r, http.StatusOK, o)
}

func castEcommerceRepositoryOrMemory(repo any) ecommerceRepository {
	if r, ok := repo.(ecommerceRepository); ok {
		return r
	}
	return newMemoryEcommerceRepository()
}

func userIDKey(userID int64, key string) string {
	return fmt.Sprintf("%d|%s", userID, strings.TrimSpace(key))
}
