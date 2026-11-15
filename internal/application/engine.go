// Package application defines use cases and ports, without adapter dependencies.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

var (
	ErrInvalid             = errors.New("invalid request")
	ErrNotFound            = errors.New("not found")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrAccountExists       = errors.New("account exists")
	ErrInvalidCursor       = errors.New("invalid cursor")
)

type RegisterInput struct {
	Protocol string
	Address  string
	Label    string
}

type AccountLabelUpdate struct {
	AccountID, ExpectedRevisionID, RevisionID domain.ID
	Label                                     string
	At                                        time.Time
}

const maxAccountLabelLength = 120

type Operation struct {
	ID         domain.ID
	ResourceID domain.ID
	CreatedAt  time.Time
	Kind       string
}

type Registration struct {
	Account     domain.Account
	Operation   Operation
	Fingerprint string
}

type RegistrationResult struct {
	Account  domain.Account
	Replayed bool
}

type Event struct {
	Sequence      int64
	Account       *domain.Account
	Conversation  *domain.Conversation
	Message       *domain.Message
	Connection    *domain.Connection
	History       *domain.HistoryStatus
	HistoryPruned *domain.HistoryPruned
	RoomSession   *domain.RoomSession
	Avatar        *domain.Avatar
	Preview       *domain.MessagePreview
	Star          *domain.MessageStar
	Type          string
	OccurredAt    time.Time

	RemovedAccountID domain.ID
}

type Snapshot struct {
	ID       domain.ID
	Sequence int64
	Role     string
}

type Repository interface {
	Ping(context.Context) error
	Snapshot(context.Context) (domain.ID, int64, error)
	Register(context.Context, Registration) (RegistrationResult, error)
	SetAccountLabel(context.Context, AccountLabelUpdate) (domain.Account, error)
	Account(context.Context, domain.ID) (domain.Account, error)
	Accounts(context.Context, domain.ID, int) ([]domain.Account, error)
	Operation(context.Context, domain.ID) (Operation, error)
	Events(context.Context, int64, int) ([]Event, error)
}

type NewID func() (domain.ID, error)

// EngineDiagnostics observes operational outcomes without receiving user data
// or arbitrary errors. It is installed by the application host before Run.
type EngineDiagnostics interface {
	ReportSendOutcome(state string, elapsed time.Duration)
	ReportMediaFailure()
	ReportRetentionFailure()
}

type Engine struct {
	diagnostics       EngineDiagnostics
	repo              Repository
	role              string
	newID             NewID
	now               func() time.Time
	protocols         Protocols
	avatarFiles       AvatarFiles
	previewFetcher    PreviewFetcher
	mediaReader       MediaReader
	mediaFiles        MediaFiles
	previewWork       chan struct{}
	draftPreviewSlots chan struct{}
	work              chan struct{}
	attachmentWork    chan struct{}
	roomWork          chan struct{}
	retentionWork     chan struct{}
	eventMu           sync.Mutex
	eventWake         chan struct{}

	accountConnectionMu sync.Mutex
}

func New(repo Repository, role string, ids NewID, now func() time.Time) (*Engine, error) {
	if role != "local" && role != "hosted" {
		return nil, ErrInvalid
	}
	return &Engine{
		repo: repo, role: role, newID: ids, now: now,
		work: make(chan struct{}, 1), attachmentWork: make(chan struct{}, 1), roomWork: make(chan struct{}, 1), retentionWork: make(chan struct{}, 1),
		previewWork: make(chan struct{}, 1), draftPreviewSlots: make(chan struct{}, 2), eventWake: make(chan struct{}),
	}, nil
}

func (e *Engine) SetDiagnostics(observer EngineDiagnostics) { e.diagnostics = observer }

func (e *Engine) Ping(ctx context.Context) error { return e.repo.Ping(ctx) }

func (e *Engine) Snapshot(ctx context.Context) (Snapshot, error) {
	id, seq, err := e.repo.Snapshot(ctx)
	return Snapshot{ID: id, Sequence: seq, Role: e.role}, err
}

func (e *Engine) Register(ctx context.Context, key domain.ID, input RegisterInput) (RegistrationResult, error) {
	if _, err := domain.ParseID(string(key)); err != nil {
		return RegistrationResult{}, ErrInvalid
	}
	input.Protocol = strings.TrimSpace(input.Protocol)
	input.Address = strings.TrimSpace(input.Address)
	if e.protocols != nil {
		var err error
		input.Address, err = e.canonical(input.Protocol, input.Address)
		if err != nil {
			return RegistrationResult{}, err
		}
	}
	input.Label = strings.TrimSpace(input.Label)
	if input.Protocol != "xmpp" || !validText(input.Address, 1023) || !validText(input.Label, maxAccountLabelLength) {
		return RegistrationResult{}, ErrInvalid
	}
	data, err := json.Marshal(input)
	if err != nil {
		return RegistrationResult{}, err
	}
	hash := sha256.Sum256(data)
	accountID, err := e.newID()
	if err != nil {
		return RegistrationResult{}, err
	}
	revisionID, err := e.newID()
	if err != nil {
		return RegistrationResult{}, err
	}
	now := e.now().UTC()
	return e.repo.Register(ctx, Registration{
		Account: domain.Account{ID: accountID, RevisionID: revisionID,
			Protocol: input.Protocol, Address: input.Address, Label: input.Label, CreatedAt: now},
		Operation:   Operation{ID: key, ResourceID: accountID, CreatedAt: now, Kind: "register_account"},
		Fingerprint: hex.EncodeToString(hash[:]),
	})
}

func validText(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) > 0 &&
		utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}

func (e *Engine) SetAccountLabel(ctx context.Context, id, expected domain.ID, label string) (domain.Account, error) {
	label = strings.TrimSpace(label)
	if !validID(id) || !validID(expected) || !validText(label, maxAccountLabelLength) {
		return domain.Account{}, ErrInvalid
	}
	revision, err := e.newID()
	if err != nil {
		return domain.Account{}, err
	}
	account, err := e.repo.SetAccountLabel(ctx, AccountLabelUpdate{
		AccountID: id, ExpectedRevisionID: expected, RevisionID: revision,
		Label: label, At: e.now().UTC(),
	})
	if err == nil && account.RevisionID == revision {
		e.notifyEvents()
	}
	return account, err
}

func (e *Engine) Account(ctx context.Context, id domain.ID) (domain.Account, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return domain.Account{}, ErrInvalid
	}
	return e.repo.Account(ctx, id)
}

func (e *Engine) Accounts(ctx context.Context, after domain.ID, limit int) ([]domain.Account, error) {
	if after != "" {
		if _, err := domain.ParseID(string(after)); err != nil {
			return nil, ErrInvalid
		}
	}
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return e.repo.Accounts(ctx, after, limit+1)
}

func (e *Engine) Operation(ctx context.Context, id domain.ID) (Operation, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return Operation{}, ErrInvalid
	}
	return e.repo.Operation(ctx, id)
}

func (e *Engine) Events(ctx context.Context, after int64) ([]Event, error) {
	if after < 0 {
		return nil, ErrInvalidCursor
	}
	return e.repo.Events(ctx, after, 100)
}

// EventWake broadcasts a hint to read the durable event log. Take the channel
// before reading Events so a concurrent commit also wakes an idle subscriber.
func (e *Engine) EventWake() <-chan struct{} {
	e.eventMu.Lock()
	defer e.eventMu.Unlock()
	return e.eventWake
}

func (e *Engine) notifyEvents() {
	e.eventMu.Lock()
	defer e.eventMu.Unlock()
	close(e.eventWake)
	e.eventWake = make(chan struct{})
}
