package raftalg

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/coreos/etcd/etcdserver/stats"
	"github.com/coreos/etcd/pkg/types"
	"github.com/coreos/etcd/raft"
	"github.com/coreos/etcd/raft/raftpb"
	"github.com/coreos/etcd/rafthttp"
	"github.com/coreos/etcd/wal"
	"github.com/coreos/etcd/wal/walpb"
	"golang.org/x/sync/errgroup"
)

type RaftAlg struct {
	commitC         chan string
	doneRestoreLogC chan struct{}

	node        raft.Node
	raftStorage *raft.MemoryStorage
	wal         *wal.WAL
	transport   *rafthttp.Transport

	id     int
	peers  []string
	waldir string
}

func New(id int, peers []string) *RaftAlg {
	return &RaftAlg{
		commitC:         make(chan string),
		doneRestoreLogC: make(chan struct{}),

		raftStorage: raft.NewMemoryStorage(),

		id:     id,
		peers:  peers,
		waldir: fmt.Sprintf("kvraft-%d", id),
	}
}

func (r *RaftAlg) Run(ctx context.Context) error {
	c := &raft.Config{
		ID:              uint64(r.id),
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         r.raftStorage,
		MaxSizePerMsg:   1024 * 1024,
		MaxInflightMsgs: 256,
		Logger: &raft.DefaultLogger{
			Logger: log.New(
				os.Stderr,
				"[Raft-debug]",
				0,
			),
		},
	}

	rpeers := make([]raft.Peer, len(r.peers))
	for i := range rpeers {
		rpeers[i] = raft.Peer{ID: uint64(i + 1)}
	}

	// wal保存用ディレクトリがすでにあるか確認
	oldwal := wal.Exist(r.waldir)

	// 再起動のためにWALに保存されたエントリを適用します。
	w, err := r.replayWAL(ctx) // あとで実装
	if err != nil {
		return err
	}
	r.wal = w

	if oldwal {
		r.node = raft.RestartNode(c)
	} else {
		r.node = raft.StartNode(c, rpeers)
	}

	r.transport = &rafthttp.Transport{
		ID:        types.ID(r.id),
		ClusterID: 0x1000,
		// raft.Raftインターフェース(あとでRaftAlgに実装)
		Raft: r,
		// 通信の統計を記録するために使用されます
		ServerStats: stats.NewServerStats("", ""),
		//リーダーがフォロワーとの通信の統計を記録するために使用されます
		LeaderStats: stats.NewLeaderStats(strconv.Itoa(r.id)),
		ErrorC:      make(chan error),
	}

	err = r.transport.Start()
	if err != nil {
		return err
	}

	for i := range r.peers {
		if i+1 != r.id {
			r.transport.AddPeer(
				types.ID(i+1), []string{r.peers[i]},
			)
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return r.serveRaftHTTP(ctx)
	})
	eg.Go(func() error {
		return r.serveChannels(ctx)
	})

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("tryraft: stop serving Raft: %w", err)
	}
	return nil
}

func (r *RaftAlg) Propose(prop []byte) error {
	ctx, cancel := context.WithTimeout(
		context.Background(), 3*time.Second,
	)
	defer cancel()
	return r.node.Propose(ctx, prop)
}

func (r *RaftAlg) Commit() <-chan string {
	return r.commitC
}

func (r *RaftAlg) DoneReplayWAL() <-chan struct{} {
	return r.doneRestoreLogC
}

// impliments raft.RaftNode interface -------------

func (r *RaftAlg) Process(ctx context.Context, m raftpb.Message) error {
	return r.node.Step(ctx, m)
}
func (r *RaftAlg) IsIDRemoved(id uint64) bool {
	return false
}
func (r *RaftAlg) ReportUnreachable(id uint64)                          {}
func (r *RaftAlg) ReportSnapshot(id uint64, status raft.SnapshotStatus) {}

// ------------------------------------------------

func (r *RaftAlg) replayWAL(ctx context.Context) (*wal.WAL, error) {
	// WALディレクトリの存在チェック。無ければ作る。
	if !wal.Exist(r.waldir) {
		_ = os.Mkdir(r.waldir, 0750)
		w, _ := wal.Create(r.waldir, nil)
		w.Close()
	}

	// WALに保存されたログを取得
	w, _ := wal.Open(r.waldir, walpb.Snapshot{})
	_, _, ents, _ := w.ReadAll()

	// Raftノードが適切なログからスタートできるようにエントリをログに追加
	_ = r.raftStorage.Append(ents)

	select {
	// replayの終了を通知する
	case r.doneRestoreLogC <- struct{}{}:
	case <-time.After(10 * time.Second):
		return nil, errors.New(
			"timeout(10s) receiving done restore channel",
		)
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// エントリをState machineに適用する
	_ = r.publishEntries(ctx, ents) // あとで実装

	return w, nil
}

func (r *RaftAlg) publishEntries(
	ctx context.Context, ents []raftpb.Entry,
) error {
	for i := range ents {
		if ents[i].Type != raftpb.EntryNormal ||
			len(ents[i].Data) == 0 {
			// EntryNormal 型のエントリのみをサポートする
			// 他のタイプについては後述
			continue
		}
		s := string(ents[i].Data)

		select {
		// 適用して良いエントリをチャネル経由で通知する
		case r.commitC <- s:
		case <-time.After(10 * time.Second):
			return errors.New(
				"timeout(10s) sending committed channel",
			)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *RaftAlg) serveRaftHTTP(ctx context.Context) error {
	url, err := url.Parse(r.peers[r.id-1])
	if err != nil {
		return fmt.Errorf("failed parsing URL: %w", err)
	}
	srv := http.Server{Addr: url.Host, Handler: r.transport.Handler()}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return srv.ListenAndServe()
	})

	// コンテキストキャンセルを受けたらシャットダウン
	<-ctx.Done()
	sCtx, sCancel := context.WithTimeout(
		context.Background(), 10*time.Second,
	)
	defer sCancel()
	if err := srv.Shutdown(sCtx); err != nil {
		return err
	}

	return eg.Wait()
}

func (r *RaftAlg) serveChannels(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.node.Tick()

		case rd := <-r.node.Ready():
			// WALとメモリストレージにエントリを保存
			_ = r.wal.Save(rd.HardState, rd.Entries)
			_ = r.raftStorage.Append(rd.Entries)

			r.transport.Send(rd.Messages)

			// コミット済みエントリを通知
			err := r.publishEntries(ctx, rd.CommittedEntries)
			if err != nil {
				return err
			}
			r.node.Advance()

		case err := <-r.transport.ErrorC:
			return err

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
