package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

type Store interface {
	Lookup(key string) (string, bool)
	Save(k string, v string) error
	Conf(op string, id uint64, url string) error
}

type Request struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ConfigRequest struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type handler struct {
	store Store
}

func (h *handler) Get(c *gin.Context) {
	key := c.Param("key")
	v, ok := h.store.Lookup(key)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "not found",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		key: v,
	})
}

func (h *handler) Put(c *gin.Context) {
	var req Request
	c.BindJSON(&req)
	err := h.store.Save(req.Key, string(req.Value))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		req.Key: string(req.Value),
	})
}

func (h *handler) Post(c *gin.Context) {
	var req ConfigRequest
	c.BindJSON(&req)

	nodeID, err := strconv.ParseUint(req.ID, 0, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	err = h.store.Conf("add", nodeID, req.URL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"add node": "ok",
	})
}

func (h *handler) Delete(c *gin.Context) {
	var req ConfigRequest
	c.BindJSON(&req)
	nodeID, err := strconv.ParseUint(req.ID, 0, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	err = h.store.Conf("remove", nodeID, req.URL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"remove node": "ok",
	})
}

type Server struct {
	server http.Server
}

func New(port int, kv Store) *Server {
	h := &handler{
		store: kv,
	}
	r := gin.Default()
	r.GET("/:key", h.Get)
	r.PUT("/", h.Put)
	r.POST("/", h.Post)
	r.DELETE("/", h.Delete)

	return &Server{
		server: http.Server{
			Addr:    ":" + strconv.Itoa(port),
			Handler: r,
		},
	}
}

func (s *Server) Run(ctx context.Context) error {
	// errorを返したらコンテキストをキャンセル
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return s.server.ListenAndServe()
	})

	// コンテキストキャンセルを受けたらサーバーのシャットダウン
	<-ctx.Done()
	sCtx, sCancel := context.WithTimeout(
		context.Background(), 10*time.Second,
	)
	defer sCancel()
	if err := s.server.Shutdown(sCtx); err != nil {
		return err
	}

	// gorutineが終了するまで待つ
	return eg.Wait()
}
