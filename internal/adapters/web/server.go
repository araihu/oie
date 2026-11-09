// Package web is the Goshtoso BFF. Its only engine dependency is the generated HTTP client.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso/expressions"
	"github.com/araihu/oie/internal/adapters/cachehttp"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"

	"golang.org/x/text/language"
)

//go:embed app.css
var stylesheet []byte

//go:embed chat-pattern.svg
var chatPattern []byte

//go:embed actions.js
var actionsScript []byte

//go:embed conversation-preferences.js
var conversationPreferencesScript []byte

//go:embed stars.js
var starsScript []byte

//go:embed formatting.js
var formattingScript []byte

//go:embed theme.js
var themeScript []byte

//go:embed locales/*.json
var localeFiles embed.FS

type Server struct {
	devBuildID   string
	client       *engineapi.ClientWithResponses
	streamClient *engineapi.Client
	catalogs     map[string]copy
}

func New(engineURL string, options ...Option) (*Server, error) {
	u, err := url.Parse(engineURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, &url.Error{Op: "configure engine", URL: "", Err: errInvalidURL{}}
	}
	catalogs, err := loadCatalogs()
	if err != nil {
		return nil, err
	}
	client, err := engineapi.NewClientWithResponses(engineURL, engineapi.WithHTTPClient(&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}))
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 5 * time.Second
	transport.TLSHandshakeTimeout = 5 * time.Second
	streamClient, err := engineapi.NewClient(engineURL, engineapi.WithHTTPClient(&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}))
	if err != nil {
		return nil, err
	}
	server := &Server{client: client, streamClient: streamClient, catalogs: catalogs}
	for _, option := range options {
		if option != nil {
			option(server)
		}
	}
	return server, nil
}

type errInvalidURL struct{}

func (errInvalidURL) Error() string {
	return "expected an HTTP(S) engine base URL without credentials, query, or fragment"
}

type messageView struct {
	engineapi.Message
	Presentation      string
	PresentationKnown bool
	VideosKnown       bool
	Formatted         templ.Component
	Attachment        *mediaView
}
type mediaView struct{ Kind, Title, Description, PreviewUrl string }
type connectionView struct {
	Known bool
	Value engineapi.Connection
}
type view struct {
	SenderInfo                                                                                    *senderInfoView
	GlobalMessageSearch                                                                           bool
	ConversationSearchOpen                                                                        bool
	ConversationSearchQuery                                                                       string
	ConversationSearchHistory                                                                     bool
	MessageSearch                                                                                 messageSearchView
	NativeMessageSearch                                                                           bool
	PreferenceRecovery                                                                            bool
	DevBuildID                                                                                    string
	RoomSession                                                                                   *engineapi.RoomSession
	MessagingCapabilities                                                                         *engineapi.Capabilities
	InfoView, InfoType, InfoCursor, InfoQuery, InfoFocus                                          string
	InfoShared                                                                                    *engineapi.SharedContentPage
	InfoStars                                                                                     *engineapi.StarredMessagePage
	InfoInvalid, SharedInDetails                                                                  bool
	SharedType, SharedCursor, SharedConversation                                                  string
	SharedContent                                                                                 *engineapi.SharedContentPage
	SharedInvalid                                                                                 bool
	SharedCard                                                                                    bool
	Locale, Theme, Role                                                                           string
	Text                                                                                          copy
	Avatars                                                                                       map[avatarIdentity]string
	AvatarAccounts                                                                                []string
	Accounts                                                                                      []engineapi.Account
	AccountUnread                                                                                 map[string]int
	AccountPage, ManagedAccount                                                                   string
	CapabilityEvidence                                                                            *engineapi.Capabilities
	ConversationInfo                                                                              *engineapi.ConversationDetails
	Connections                                                                                   map[string]connectionView
	Histories                                                                                     map[string]engineapi.HistoryStatus
	Next                                                                                          string
	Unavailable, NotFound, Preview, ChatOpen                                                      bool
	FocusMessage                                                                                  string
	ContextHasNewer, ContextHasOlder, ContextMissing                                              bool
	TimelineOlder, TimelineNewer, TimelineComplete                                                bool
	TimelineMode, TimelineDirection, TimelineCursor, TimelineRequestOldest, TimelineRequestNewest string
	TimelineCanonicalCursor                                                                       string
	TimelineAliases                                                                               map[string]messageView
	Section, Filter, Query, ChatID, AccountFilter                                                 string
	Chats                                                                                         []engineapi.Conversation
	Selected                                                                                      *engineapi.Conversation
	Messages                                                                                      []messageView
	StarsKnown                                                                                    bool
	Stars                                                                                         map[string]engineapi.MessageStar
	PreviewsKnown                                                                                 bool
	MessagePreviews                                                                               map[string][]engineapi.MessagePreview
	MessageVideos                                                                                 map[string][]engineapi.SharedContentItem
	EngineID, EventCursor, CSRF, ErrorMessage, FormTarget                                         string
	Values                                                                                        map[string]string
}

type copy struct {
	ViewEditHistory, EditHistory, EditHistoryLoading, EditHistoryUnavailable, EditHistoryEmpty, EditHistoryOriginal, EditHistoryCurrent, EditHistoryPrevious, EditHistoryMore, EditHistoryNewer, EditHistoryChanged string
	SenderInfo, OpenSenderInfo, SenderInfoLoading, SenderInfoUnavailable, SenderConversation, SenderAccount                                                                                                         string
	SearchContactsCoverage, SearchContacts, SearchContactsPartial, SearchContactsLimited                                                                                                                            string

	SearchEverything, SearchConversations, SearchMessagesGroup, SearchConversationsLimited                                         string
	ConversationSearchOlder, ConversationSearchNewer, ConversationSearchClose, ConversationSearchResult, ConversationSearchMissing string

	SearchMessages, SearchSavedMessages, SearchQuery, IncludeEditHistory, SearchHistoryHelp, SearchPriorVersion, SearchMetadata, SearchBlankTitle, SearchBlankBody, SearchEmptyTitle, SearchEmptyBody, SearchLoading, SearchFailed, SearchInvalid, SearchMissing, SearchRestart, SearchScope, SearchAllConversations, SearchMetadataPending, SearchMore string

	ArchiveConversation, UnarchiveConversation, FavoriteConversation, UnfavoriteConversation                                                                                                                 string
	RetryConversationPreference, ConversationPreferenceUpdated, ConversationPreferenceConflict, ConversationPreferenceUncertain, ConversationPreferenceUnavailable, ConversationPreferenceStorageUnavailable string
	CapabilityMentions                                                                                                                                                                                       string
	MentionUsers, MentionLoading, MentionEmpty, MentionFailed, MentionPartial                                                                                                                                string
	MessageStarred                                                                                                                                                                                           string
	AutoVideoThumbnails, AutoVideoThumbnailsHelp, VideoThumbnailLoad, VideoThumbnailLoading, VideoThumbnailFailed                                                                                            string
	ReadFailed                                                                                                                                                                                               string
	TimelineOlder, TimelineNewer, TimelineLoading, TimelineFailed, TimelinePaused                                                                                                                            string
	SharedItemActions                                                                                                                                                                                        string
	SharedCopyLink                                                                                                                                                                                           string
	SharedDownload                                                                                                                                                                                           string
	SharedActionFailed                                                                                                                                                                                       string
	SharedLinkCopied                                                                                                                                                                                         string
	SharedDownloading                                                                                                                                                                                        string
	RoomActions                                                                                                                                                                                              string
	DocumentOpen                                                                                                                                                                                             string
	DocumentViewer                                                                                                                                                                                           string
	DocumentDownload                                                                                                                                                                                         string
	DocumentOriginal                                                                                                                                                                                         string
	DocumentLoading                                                                                                                                                                                          string
	DocumentLoadFailed                                                                                                                                                                                       string
	DocumentRetry                                                                                                                                                                                            string
	DocumentUnsupported                                                                                                                                                                                      string
	DocumentEmpty                                                                                                                                                                                            string
	DocumentPrevious                                                                                                                                                                                         string
	DocumentNext                                                                                                                                                                                             string
	DocumentPage                                                                                                                                                                                             string
	DocumentFit                                                                                                                                                                                              string
	DocumentZoomIn                                                                                                                                                                                           string
	DocumentZoomOut                                                                                                                                                                                          string
	EmojiNoRecent                                                                                                                                                                                            string
	EmojiVariants                                                                                                                                                                                            string
	EmojiVariantsFor                                                                                                                                                                                         string
	EmojiPicker                                                                                                                                                                                              string
	EmojiSearch                                                                                                                                                                                              string
	EmojiCategories                                                                                                                                                                                          string
	EmojiAll                                                                                                                                                                                                 string
	EmojiRecent                                                                                                                                                                                              string
	EmojiMore                                                                                                                                                                                                string
	EmojiLoading                                                                                                                                                                                             string
	EmojiEmpty                                                                                                                                                                                               string
	EmojiFailed                                                                                                                                                                                              string
	EmojiSmileys                                                                                                                                                                                             string
	EmojiPeople                                                                                                                                                                                              string
	EmojiAnimals                                                                                                                                                                                             string
	EmojiFood                                                                                                                                                                                                string
	EmojiTravel                                                                                                                                                                                              string
	EmojiActivities                                                                                                                                                                                          string
	EmojiObjects                                                                                                                                                                                             string
	EmojiSymbols                                                                                                                                                                                             string
	EmojiFlags                                                                                                                                                                                               string
	RefreshPreview                                                                                                                                                                                           string
	RefreshPreviewFailed                                                                                                                                                                                     string
	SharedLoadingOlder                                                                                                                                                                                       string
	SharedLoadFailed                                                                                                                                                                                         string
	VideoPlay                                                                                                                                                                                                string
	VideoRetry                                                                                                                                                                                               string
	VideoLoading                                                                                                                                                                                             string
	VideoPlaying                                                                                                                                                                                             string
	VideoPaused                                                                                                                                                                                              string
	VideoReady                                                                                                                                                                                               string
	VideoUnavailable                                                                                                                                                                                         string
	VideoUnsupported                                                                                                                                                                                         string
	AppName                                                                                                                                                                                                  string
	AppDescription                                                                                                                                                                                           string
	CapabilityEditMessagesNote                                                                                                                                                                               string
	Edited                                                                                                                                                                                                   string
	ReplyMessage                                                                                                                                                                                             string
	CancelReply                                                                                                                                                                                              string
	ReplyUnavailable                                                                                                                                                                                         string
	ReplyPreparing                                                                                                                                                                                           string
	ReplyLocked                                                                                                                                                                                              string
	RoomJoined                                                                                                                                                                                               string
	RoomJoining                                                                                                                                                                                              string
	RoomJoinFailed                                                                                                                                                                                           string
	RoomJoinRequired                                                                                                                                                                                         string
	JoinRoom                                                                                                                                                                                                 string
	LeaveRoom                                                                                                                                                                                                string
	RoomNickname                                                                                                                                                                                             string
	RoomNicknameConflict                                                                                                                                                                                     string
	RoomForbidden                                                                                                                                                                                            string
	RoomNotAuthorized                                                                                                                                                                                        string
	RoomJoinTimeout                                                                                                                                                                                          string
	RoomUnavailable                                                                                                                                                                                          string
	RoomCommandUnknown                                                                                                                                                                                       string
	RoomCommandFailed                                                                                                                                                                                        string
	RoomConflict                                                                                                                                                                                             string
	RoomJoinRequested                                                                                                                                                                                        string
	RoomLeaveRequested                                                                                                                                                                                       string
	RoomQueueHint                                                                                                                                                                                            string
	NavLoading                                                                                                                                                                                               string
	NavFailed                                                                                                                                                                                                string
	InfoSharedContent                                                                                                                                                                                        string
	InfoStarred                                                                                                                                                                                              string
	InfoConnection                                                                                                                                                                                           string
	InfoBack                                                                                                                                                                                                 string
	InfoGroup                                                                                                                                                                                                string
	InfoReportedOccupants                                                                                                                                                                                    string
	InfoStarsUnavailable                                                                                                                                                                                     string
	InfoStarsEmpty                                                                                                                                                                                           string
	InfoNavigationFailed                                                                                                                                                                                     string
	SharedAccount                                                                                                                                                                                            string
	SearchAccounts                                                                                                                                                                                           string
	NoAccountsFound                                                                                                                                                                                          string
	SharedTools                                                                                                                                                                                              string
	SharedContentTypes                                                                                                                                                                                       string
	SharedMediaTab                                                                                                                                                                                           string
	SharedLinksTab                                                                                                                                                                                           string
	SharedDocsTab                                                                                                                                                                                            string
	SharedAllChats                                                                                                                                                                                           string
	SharedSearch                                                                                                                                                                                             string
	SharedSearchAction                                                                                                                                                                                       string
	SharedPageUnavailable                                                                                                                                                                                    string
	SharedNewest                                                                                                                                                                                             string
	SharedUnavailable                                                                                                                                                                                        string
	SharedFinding                                                                                                                                                                                            string
	SharedEmpty                                                                                                                                                                                              string
	SharedEmptyHelp                                                                                                                                                                                          string
	SharedGoToMessage                                                                                                                                                                                        string
	SharedOpenLink                                                                                                                                                                                           string
	SharedNext                                                                                                                                                                                               string
	SharedImage                                                                                                                                                                                              string
	SharedAudio                                                                                                                                                                                              string
	SharedVideo                                                                                                                                                                                              string
	SharedDocument                                                                                                                                                                                           string
	SharedLink                                                                                                                                                                                               string
	CapabilityPresenceNote                                                                                                                                                                                   string
	DetailsRefresh                                                                                                                                                                                           string
	DetailsOwningAccount                                                                                                                                                                                     string
	DetailsObservation                                                                                                                                                                                       string
	DetailsSnapshotHelp                                                                                                                                                                                      string
	DetailsPending                                                                                                                                                                                           string
	DetailsKnown                                                                                                                                                                                             string
	DetailsPartial                                                                                                                                                                                           string
	DetailsForbidden                                                                                                                                                                                         string
	DetailsUnavailable                                                                                                                                                                                       string
	DetailsNotConnected                                                                                                                                                                                      string
	DetailsUnsupported                                                                                                                                                                                       string
	DetailsUnknown                                                                                                                                                                                           string
	DetailsReadUnavailable                                                                                                                                                                                   string
	DetailsRoomName                                                                                                                                                                                          string
	DetailsDescription                                                                                                                                                                                       string
	DetailsTopic                                                                                                                                                                                             string
	DetailsRoomSession                                                                                                                                                                                       string
	RoomNotJoined                                                                                                                                                                                            string

	DetailsOccupantCount                                                                                                                                                 string
	DetailsRoomProperties                                                                                                                                                string
	DetailsPeerPresence                                                                                                                                                  string
	DetailsPeerPresenceHelp                                                                                                                                              string
	DetailsParticipantsTruncated                                                                                                                                         string
	DetailsNoParticipants                                                                                                                                                string
	DetailsSourceDiscovery                                                                                                                                               string
	DetailsSourcePresence                                                                                                                                                string
	DetailsObservedAt                                                                                                                                                    string
	UnknownParticipant                                                                                                                                                   string
	PresenceAvailable                                                                                                                                                    string
	PresenceAway                                                                                                                                                         string
	PresenceBusy                                                                                                                                                         string
	PresenceExtendedAway                                                                                                                                                 string
	PresenceChat                                                                                                                                                         string
	PresenceUnavailable                                                                                                                                                  string
	PresenceUnknown                                                                                                                                                      string
	RoomPublic                                                                                                                                                           string
	RoomHidden                                                                                                                                                           string
	RoomMembersOnly                                                                                                                                                      string
	RoomOpen                                                                                                                                                             string
	RoomPersistent                                                                                                                                                       string
	RoomTemporary                                                                                                                                                        string
	RoomModerated                                                                                                                                                        string
	RoomUnmoderated                                                                                                                                                      string
	RoomPasswordProtected                                                                                                                                                string
	RoomUnsecured                                                                                                                                                        string
	RoomAnonymous                                                                                                                                                        string
	RoomNonAnonymous                                                                                                                                                     string
	CapabilityPartialDetails                                                                                                                                             string
	Capabilities                                                                                                                                                         string
	CapabilitiesIntro                                                                                                                                                    string
	BackToAccount                                                                                                                                                        string
	CapabilityFeature                                                                                                                                                    string
	CapabilityApp                                                                                                                                                        string
	CapabilityProtocol                                                                                                                                                   string
	CapabilityServer                                                                                                                                                     string
	CapabilityScope                                                                                                                                                      string
	CapabilitySupported                                                                                                                                                  string
	CapabilityPartial                                                                                                                                                    string
	CapabilityUnsupported                                                                                                                                                string
	CapabilityNotImplemented                                                                                                                                             string
	CapabilityNotRequired                                                                                                                                                string
	CapabilityScopeDependent                                                                                                                                             string
	CapabilityNotAdvertised                                                                                                                                              string
	CapabilityUnknown                                                                                                                                                    string
	CapabilityScopeLocal                                                                                                                                                 string
	CapabilityScopeAccount                                                                                                                                               string
	CapabilityScopeServer                                                                                                                                                string
	CapabilityScopeService                                                                                                                                               string
	CapabilityScopePeer                                                                                                                                                  string
	CapabilityScopeRoom                                                                                                                                                  string
	CapabilityNotConnected                                                                                                                                               string
	CapabilityDiscoveryPending                                                                                                                                           string
	CapabilityDiscoveryComplete                                                                                                                                          string
	CapabilityDiscoveryPartial                                                                                                                                           string
	CapabilityDiscoveryFailed                                                                                                                                            string
	CapabilityDiscoveryKnown                                                                                                                                             string
	CapabilityChecked                                                                                                                                                    string
	CapabilityEvidenceHelp                                                                                                                                               string
	CapabilityReadUnavailable                                                                                                                                            string
	CapabilityRefresh                                                                                                                                                    string
	CapabilityServices                                                                                                                                                   string
	CapabilityServicesHelp                                                                                                                                               string
	CapabilityNoServices                                                                                                                                                 string
	CapabilityIdentities                                                                                                                                                 string
	CapabilityNamespaces                                                                                                                                                 string
	CapabilityNoNamespaces                                                                                                                                               string
	CapabilityDirectText                                                                                                                                                 string
	CapabilityAccountHistory                                                                                                                                             string
	CapabilitySavedRooms                                                                                                                                                 string
	CapabilityRoomHistory                                                                                                                                                string
	CapabilityRoomDiscovery                                                                                                                                              string
	CapabilityRoomMessaging                                                                                                                                              string
	CapabilityFileUpload                                                                                                                                                 string
	CapabilityImagePreviews                                                                                                                                              string
	CapabilityLinkPreviews                                                                                                                                               string
	CapabilityReceivedReplies                                                                                                                                            string
	CapabilitySendReplies                                                                                                                                                string
	CapabilityEditMessages                                                                                                                                               string
	CapabilityRetractMessages                                                                                                                                            string
	CapabilityReactions                                                                                                                                                  string
	CapabilityReadReceipts                                                                                                                                               string
	CapabilityTyping                                                                                                                                                     string
	CapabilityPresence                                                                                                                                                   string
	CapabilityProfileImages                                                                                                                                              string
	CapabilityFormatting                                                                                                                                                 string
	CapabilityE2EE                                                                                                                                                       string
	CapabilityStars                                                                                                                                                      string
	CapabilitySchedules                                                                                                                                                  string
	CapabilitySync                                                                                                                                                       string
	CapabilitySavedRoomsNote                                                                                                                                             string
	CapabilityProfileImagesNote                                                                                                                                          string
	CapabilityFormattingNote                                                                                                                                             string
	CapabilityReceivedRepliesNote                                                                                                                                        string
	CapabilityLocalNote                                                                                                                                                  string
	Accounts, EmptyTitle, EmptyBody, NotConnected, Conversations                                                                                                         string
	Unavailable, UnavailableBody, Retry, Local, Hosted                                                                                                                   string
	Skip, Languages, Appearance, System, Light                                                                                                                           string
	Dark, Next, English, Portuguese, NotFound                                                                                                                            string
	NotFoundBody, Chats, Favorites, Archived, SharedMedia                                                                                                                string
	HistorySyncing, HistoryPartial, HistoryUnsupported, HistoryComplete, HistoryCount, RoomReadOnly                                                                      string
	SwitchAccount, ManageAccounts                                                                                                                                        string
	BackToAccounts, AdvancedConnection, AccountMissing, AddAccountHelp                                                                                                   string
	Settings, You, All, Unread, Groups                                                                                                                                   string
	SearchChats, NoResults, NoResultsBody, NewChat, More                                                                                                                 string
	Preview, PreviewNote, Message, Send, Attach                                                                                                                          string
	SendingUnavailable, Back, Today, Navigation, Active                                                                                                                  string
	OpenNavigation, Close, Details, Participants, MediaEmpty                                                                                                             string
	MediaBody, SelectChat, SelectChatBody, EngineState, AccountHelp                                                                                                      string
	PreviewOnly, ThemeHelp, LanguageHelp, NoChats, NoChatsBody                                                                                                           string
	AddAccount, AccountAddress, AccountLabel, AddressPlaceholder, LabelPlaceholder                                                                                       string
	RegisterAccount, Password, PasswordHelp, Endpoint, EndpointHelp                                                                                                      string
	Connect, Disconnect, Connected, Connecting, ConnectionError                                                                                                          string
	ConnectionUnknown, AuthFailed, TLSFailed, ConnectionFailed, ChooseAccount                                                                                            string
	AllAccounts, PeerAddress, StartConversation, AccountSaved, ConnectionRequested                                                                                       string
	Disconnected, Queued, Dispatching, Submitted, UnknownSend                                                                                                            string
	FailedSend, Cancelled, SubmittedHint, UnknownHint, CancelSend                                                                                                        string
	CancelConflict, CancelSuccess, MessageAccepted, PendingAcceptance, RetrySubmission                                                                                   string
	SendPending, SendUnconfirmed, SendAccepted                                                                                                                           string
	Saving, InvalidForm, InvalidMessage, CSRFError, OperationConflict                                                                                                    string
	AccountExists, EngineChanged, LiveConnecting, LiveReady, LiveRetry                                                                                                   string
	NewMessages, QueueHelp, DraftStorageUnavailable, DraftLocked, MessageUnavailable                                                                                     string
	MediaUnavailable, NativeError, NewerHistory, ShowPassword, ChooseOption                                                                                              string
	PreviewLoading, PreviewUnavailable, LoadImage, LoadingImage, OpenImage, ImageView, ImageDownloadFailed, ImageTooLarge, ImageBlocked, ImageUnsupported, ImageNotFound string
	UnknownOriginal, OriginalUnavailable, JumpToLatest                                                                                                                   string
	MessageActions, ImageActions, SaveOriginalImage                                                                                                                      string
	StarMessage, UnstarMessage, RetryStar, StarConflict, StarUncertain, StarUnavailable, StarRefreshed, StarStorageUnavailable                                           string
	ImageInfo, ImageSource, ImageFilename, ImageType, ImageSize, ImageNotDownloaded                                                                                      string
	RenderMarkdown, RenderMarkdownHelp, CopyOriginal, MessageCopied, CopyFailed                                                                                          string
	DevicePreferences, AutoImageDownloads, AutoImageDownloadsHelp, DevicePreferenceUnavailable                                                                           string
	ValidationFailed, Edit, Done                                                                                                                                         string
}

func loadCatalogs() (map[string]copy, error) {
	out := make(map[string]copy)
	for _, locale := range []string{"en", "pt-BR"} {
		data, err := localeFiles.ReadFile("locales/" + locale + ".json")
		if err != nil {
			return nil, err
		}
		var text copy
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&text); err != nil {
			return nil, err
		}
		out[locale] = text
	}
	return out, nil
}

//go:embed previews.js
var previewsScript []byte

//go:embed runtime.js
var runtimeScript []byte

//go:embed timeline.js
var timelineScript []byte

//go:embed timeline.css
var timelineStylesheet []byte

//go:embed shared-actions.js
var sharedActionsScript []byte

//go:embed message-star.css
var messageStarStylesheet []byte

//go:embed shared-actions.css
var sharedActionsStylesheet []byte

//go:embed read-state.js
var readStateScript []byte

//go:embed navigation.js
var navigationScript []byte

//go:embed shared-content.js
var sharedScript []byte

//go:embed documents.js
var documentsScript []byte

//go:embed document.css
var documentStylesheet []byte

//go:embed videos.js
var videosScript []byte

//go:embed video.css
var videosStylesheet []byte

//go:embed live.js
var liveScript []byte

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/conversations/") && strings.HasSuffix(r.URL.Path, "/read-state") {
		s.readState(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/previews/") {
		s.previewStatus(w, r)
		return
	}
	if s.serveDevReload(w, r) || s.servePDFJS(w, r) {
		return
	}
	if r.URL.Path == "/ui/message-search" {
		s.messageSearch(w, r)
		return
	}
	if r.URL.Path == "/ui/document-markdown" {
		s.documentMarkdown(w, r)
		return
	}
	if isMutationPath(r.URL.Path) && r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/ui/") && r.Method == http.MethodPost {
		s.mutate(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if serveEmojiAsset(w, r) {
		return
	}
	if s.serveBrand(w, r) {
		return
	}
	assets := map[string]struct {
		mime string
		body []byte
	}{
		"/ui/mentions.js": {"text/javascript", mentionsScript}, "/ui/mentions.css": {"text/css", mentionsStylesheet},
		"/ui/timeline.js": {"text/javascript", timelineScript}, "/ui/timeline.css": {"text/css", timelineStylesheet},
		"/ui/cache.js": {"text/javascript", cacheScript}, "/ui/cache/LICENSE.md": {"text/plain; charset=utf-8", cacheLibraryLicense}, "/ui/cache/lru-cache.min.mjs": {"text/javascript", cacheLibrary}, "/ui/documents.js": {"text/javascript", documentsScript}, "/ui/document.css": {"text/css", documentStylesheet},
		"/ui/video-thumbnails.js": {"text/javascript", videoThumbnailsScript}, "/ui/videos.js": {"text/javascript", videosScript}, "/ui/video.css": {"text/css", videosStylesheet},
		"/ui/message-star.css": {"text/css", messageStarStylesheet}, "/ui/shared-actions.js": {"text/javascript", sharedActionsScript}, "/ui/shared-actions.css": {"text/css", sharedActionsStylesheet}, "/ui/read-state.js": {"text/javascript", readStateScript}, "/ui/runtime.js": {"text/javascript", runtimeScript}, "/ui/navigation.js": {"text/javascript", navigationScript}, "/ui/shared-content.js": {"text/javascript", sharedScript}, "/ui/actions.js": {"text/javascript", actionsScript}, "/ui/stars.js": {"text/javascript", starsScript}, "/ui/message-history.js": {"text/javascript", messageHistoryScript}, "/ui/message-history.css": {"text/css", messageHistoryStylesheet}, "/ui/sender.js": {"text/javascript", senderScript}, "/ui/message-search.js": {"text/javascript", messageSearchScript}, "/ui/message-search.css": {"text/css", messageSearchStylesheet}, "/ui/conversation-search.js": {"text/javascript", conversationSearchScript}, "/ui/conversation-search.css": {"text/css", conversationSearchStylesheet}, "/ui/conversation-preferences.js": {"text/javascript", conversationPreferencesScript}, "/ui/formatting.js": {"text/javascript", formattingScript}, "/ui/previews.js": {"text/javascript", previewsScript}, "/ui/app.css": {"text/css", stylesheet}, "/ui/chat-pattern.svg": {"image/svg+xml", chatPattern}, "/ui/preview-landscape.svg": {"image/svg+xml", previewLandscape}, "/ui/theme.js": {"text/javascript", themeScript}, "/ui/live.js": {"text/javascript", liveScript},
	}
	if asset, ok := assets[r.URL.Path]; ok {
		w.Header().Set("Content-Type", asset.mime)
		w.Header().Set("ETag", cachehttp.Digest(asset.body))
		cachehttp.Revalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(asset.body) })).ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/ui/timeline" {
		s.timelinePage(w, r)
		return
	}
	if r.URL.Path == "/ui/message-history" {
		s.messageEditHistory(w, r)
		return
	}
	if r.URL.Path == "/ui/sender" {
		s.senderInformation(w, r)
		return
	}
	if r.URL.Path == "/ui/mentions" {
		s.mentionCandidates(w, r)
		return
	}
	if r.URL.Path == "/ui/reply-source" {
		s.prepareReply(w, r)
		return
	}
	if r.URL.Path == "/ui/message-star" {
		s.readStar(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/ui/media/") {
		cachehttp.PrivateImmutable.Wrap(http.HandlerFunc(s.mediaImage)).ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/shared-content/") && strings.HasSuffix(r.URL.Path, "/thumbnail") {
		s.videoThumbnailStatus(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/ui/shared-content/") {
		handler := http.Handler(http.HandlerFunc(s.sharedMediaContent))
		if validOperationKey(r.URL.Query().Get("engineId")) && !r.URL.Query().Has("download") {
			handler = cachehttp.PrivateRevalidate.Wrap(handler)
		}
		handler.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/ui/avatars/") {
		cachehttp.PrivateImmutable.Wrap(http.HandlerFunc(s.avatarImage)).ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/ui/events" {
		s.events(w, r)
		return
	}
	token := csrfToken(w, r)
	v := s.baseView(r)
	v.CSRF = token
	if r.URL.Path != "/" && r.URL.Path != "/search" && r.URL.Path != "/ui/refresh" && r.URL.Path != "/ui/info" {
		v.NotFound = true
		v.ChatOpen = true
		s.render(w, r, v)
		return
	}
	savePreference(w, r, "lang", v.Locale)
	savePreference(w, r, "theme", v.Theme)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	s.load(ctx, r, &v)
	if r.URL.Path == "/" && r.URL.Query().Get("open") == "sender" && v.Selected != nil {
		v.SenderInfo, _ = s.loadSenderInformation(ctx, &v, v.EngineID, v.Selected.AccountId, v.Selected.Id, r.URL.Query().Get("senderMessage"))
	}
	if r.URL.Path == "/" && v.GlobalMessageSearch && (r.Header.Get("X-Oie-Fragment") == "" || r.Header.Get("X-Oie-Fragment") == "app") && !v.Unavailable {
		s.loadMessageSearch(ctx, &v)
	}
	if r.URL.Path == "/ui/info" {
		var buf bytes.Buffer
		if v.Selected == nil || v.Unavailable {
			w.WriteHeader(503)
			return
		}
		if err := infoFragment(v).Render(presentationContext(r.Context(), v), &buf); err != nil {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = buf.WriteTo(w)
		return
	}
	if r.URL.Path == "/ui/refresh" {
		var buf bytes.Buffer
		if err := liveRegions(v).Render(presentationContext(r.Context(), v), &buf); err != nil {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = buf.WriteTo(w)
		return
	}
	s.render(w, r, v)
}
func (s *Server) baseView(r *http.Request) view {
	v := view{DevBuildID: s.devBuildID, Locale: locale(r), Theme: preference(r, "theme", []string{"system", "light", "dark"}, "system"), Avatars: map[avatarIdentity]string{}, Connections: map[string]connectionView{}, Histories: map[string]engineapi.HistoryStatus{}, Values: map[string]string{}, MessagePreviews: map[string][]engineapi.MessagePreview{}}
	v.Text = s.catalogs[v.Locale]
	v.configureWorkspace(r)
	v.Values["address"] = r.URL.Query().Get("newChatAddress")
	v.NativeMessageSearch = r.URL.Path == "/search"
	if v.NativeMessageSearch {
		v.Section = "search"
	}
	v.configureMessageSearch(r)
	v.GlobalMessageSearch = r.URL.Query().Get("searchMode") == "messages"
	v.ConversationSearchOpen = r.URL.Query().Get("chatSearch") == "1"
	v.ConversationSearchQuery = strings.TrimSpace(r.URL.Query().Get("chatQuery"))
	v.ConversationSearchHistory = r.URL.Query().Get("chatIncludeHistory") == "true"
	return v
}
func (s *Server) load(ctx context.Context, r *http.Request, v *view) {
	editor := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Accept-Language", v.Locale)
		return nil
	}
	engine, err := s.client.GetEngineWithResponse(ctx, editor)
	if err != nil || engine.JSON200 == nil {
		v.Unavailable = true
		return
	}
	v.EngineID = engine.JSON200.Id
	v.EventCursor = engine.JSON200.EventCursor
	v.Preview = engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true"
	v.Role = v.Text.Local
	if engine.JSON200.Role == "hosted" {
		v.Role = v.Text.Hosted
	}
	limit := 100
	accounts, err := s.client.ListAccountsWithResponse(ctx, &engineapi.ListAccountsParams{Limit: &limit}, editor)
	if err != nil || accounts.JSON200 == nil {
		v.Unavailable = true
		return
	}
	v.Accounts = accounts.JSON200.Items
	if v.NativeMessageSearch {
		s.loadMessageSearch(ctx, v)
		return
	}
	v.AccountUnread = s.accountUnread(ctx)
	for _, a := range v.Accounts {
		s.loadAvatars(ctx, v, a.Id)
		if !v.Preview {
			h, err := s.client.GetAccountHistoryWithResponse(ctx, a.Id, editor)
			if err == nil && h.JSON200 != nil {
				v.Histories[a.Id] = *h.JSON200
			}
		}
		connection, err := s.client.GetAccountConnectionWithResponse(ctx, a.Id, editor)
		if err == nil && connection.JSON200 != nil {
			v.Connections[a.Id] = connectionView{Known: true, Value: *connection.JSON200}
		}
	}
	s.loadCapabilities(ctx, v)
	if v.Section == "media" {
		s.loadSharedContent(ctx, v)
		return
	}
	params := &engineapi.ListConversationsParams{Limit: &limit}
	archived := v.Section == "archived"
	params.Archived = &archived
	if v.AccountFilter != "" {
		params.AccountId = &v.AccountFilter
	}
	if v.Query != "" {
		params.Q = &v.Query
	}
	if v.Filter == "unread" {
		yes := true
		params.UnreadOnly = &yes
	}
	if v.Filter == "favorites" || v.Section == "favorites" {
		yes := true
		params.FavoritesOnly = &yes
	}
	if v.Filter == "groups" {
		kind := engineapi.ListConversationsParamsKindRoom
		params.Kind = &kind
	}
	chats, err := s.client.ListConversationsWithResponse(ctx, params, editor)
	if err != nil {
		v.Unavailable = true
		return
	}
	if chats.JSON200 == nil {
		if chats.StatusCode() != 404 {
			v.Unavailable = true
		}
		return
	}
	v.Chats = chats.JSON200.Items
	selected := v.ChatID
	if selected == "" && len(v.Chats) > 0 {
		selected = v.Chats[0].Id
	}
	if selected == "" || v.Section == "media" {
		return
	}
	conversation, err := s.client.GetConversationWithResponse(ctx, selected, editor)
	if err != nil {
		v.Unavailable = true
		return
	}
	if conversation.JSON200 == nil {
		if conversation.StatusCode() == 400 || conversation.StatusCode() == 404 {
			v.NotFound = true
		} else {
			v.Unavailable = true
		}
		return
	}
	if v.AccountFilter != "" && conversation.JSON200.AccountId != v.AccountFilter {
		v.NotFound = true
		return
	}
	v.Selected = conversation.JSON200
	v.ChatID = selected
	s.loadConversationDetails(ctx, v)
	s.loadMessaging(ctx, v)
	s.loadChatInfo(ctx, v)
	if r.URL.Path == "/ui/info" {
		return
	}
	items, usedContext := s.contextMessages(ctx, v, v.Selected.AccountId)
	if v.Unavailable {
		return
	}
	var messageBody []byte
	if !usedContext {
		messages, err := s.client.ListMessagesWithResponse(ctx, selected, timelineInitialParams(*v), editor)
		if err != nil || messages.JSON200 == nil {
			v.Unavailable = true
			return
		}
		items = messages.JSON200.Items
		v.TimelineOlder = messages.JSON200.NextCursor != nil
		v.TimelineNewer = v.TimelineCursor != "" && v.TimelineDirection != "newer"
		if v.TimelineDirection == "newer" {
			v.TimelineNewer = messages.JSON200.NextCursor != nil
			v.TimelineOlder = true
		}
		messageBody = messages.Body
	}
	if usedContext {
		v.TimelineOlder = v.ContextHasOlder
		v.TimelineNewer = v.ContextHasNewer
	}
	if !validTimelineMessages(items, v.Selected.AccountId, v.ChatID, 50) {
		v.Unavailable = true
		return
	}
	extras := map[string]*mediaView{}
	if v.Preview {
		var mock struct {
			Items []struct {
				Id         string
				Attachment *mediaView
			}
		}
		if json.Unmarshal(messageBody, &mock) == nil {
			for _, m := range mock.Items {
				extras[m.Id] = m.Attachment
			}
		}
	}
	s.formatTimeline(ctx, v, items, extras)
}
func (v view) account(id string) engineapi.Account {
	for _, a := range v.Accounts {
		if a.Id == id {
			return a
		}
	}
	return engineapi.Account{Id: id}
}
func (v view) accountLabel(id string) string {
	a := v.account(id)
	if a.Label != "" {
		return a.Label + " · " + a.Address
	}
	return a.Address
}
func (v view) connectionLabel(id string) string {
	c := v.Connections[id]
	if !c.Known {
		return v.Text.ConnectionUnknown
	}
	switch c.Value.State {
	case "connected":
		return v.Text.Connected
	case "connecting":
		return v.Text.Connecting
	case "error":
		return v.Text.ConnectionError
	default:
		return v.Text.NotConnected
	}
}
func (v view) connectionError(id string) string {
	c := v.Connections[id]
	if c.Value.LastErrorCode == nil {
		return ""
	}
	switch *c.Value.LastErrorCode {
	case "authentication_failed":
		return v.Text.AuthFailed
	case "tls_failed":
		return v.Text.TLSFailed
	default:
		return v.Text.ConnectionFailed
	}
}
func (v view) stateLabel(state engineapi.MessageSendState) string {
	switch state {
	case "queued":
		return v.Text.Queued
	case "dispatching":
		return v.Text.Dispatching
	case "submitted":
		return v.Text.Submitted
	case "unknown":
		return v.Text.UnknownSend
	case "failed":
		return v.Text.FailedSend
	case "cancelled":
		return v.Text.Cancelled
	default:
		return ""
	}
}
func (v view) timelineVersion() string {
	var out strings.Builder
	out.WriteString(v.ChatID)
	for _, m := range v.Messages {
		out.WriteString(m.Id + ":" + m.RevisionId + ":" + string(m.SendState) + ";")
		videos, _ := json.Marshal(v.messageVideos(m.Id))
		_, _ = fmt.Fprintf(&out, "video:%x;", sha256.Sum256(videos))
		reply, _ := json.Marshal([]any{m.Reply, m.Edited, m.EditedAt})
		_, _ = fmt.Fprintf(&out, "%x;", sha256.Sum256(reply))
	}
	return out.String()
}
func (v view) headerVersion() string {
	if v.Selected == nil {
		return ""
	}
	return v.Selected.Id + v.Selected.RevisionId + strconv.FormatBool(v.Selected.Archived) + strconv.FormatBool(v.Selected.Favorite) + v.Selected.Title + v.accountLabel(v.Selected.AccountId) + v.connectionLabel(v.Selected.AccountId)
}
func (v view) accountVersion() string {
	var out strings.Builder
	for _, a := range v.Accounts {
		out.WriteString(a.Id + ";")
	}
	return out.String()
}
func (v view) sendAllowed() bool {
	return v.composeAllowed() && (v.Selected.Kind == "direct" || (v.RoomSession != nil && v.RoomSession.Active && v.messagingImplemented("room_messaging")))
}
func (v view) selectedAccount() string {
	if v.AccountFilter != "" {
		return v.AccountFilter
	}
	if v.Selected != nil {
		return v.Selected.AccountId
	}
	if len(v.Accounts) > 0 {
		return v.Accounts[0].Id
	}
	return ""
}
func (v view) field(key string) string { return v.Values[key] }

func (s *Server) render(w http.ResponseWriter, r *http.Request, v view) {
	var buf bytes.Buffer
	ctx := presentationContext(r.Context(), v)
	component := page(v)
	if r.Header.Get("HX-Request") == "true" {
		component = appFragment(v)
		if r.Header.Get("X-Oie-Fragment") == "conversation" && v.Section != "media" {
			component = conversationPane(v)
		}
		if r.Header.Get("X-Oie-Fragment") == "info" && v.Selected != nil {
			component = chatInfoShell(v)
		}
	}
	if err := component.Render(ctx, &buf); err != nil {
		http.Error(w, v.Text.Unavailable, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "HX-Request, X-Oie-Fragment")
	if v.NotFound {
		w.WriteHeader(http.StatusNotFound)
	} else if v.Unavailable {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else if v.NativeMessageSearch && v.MessageSearch.Status >= 400 {
		w.WriteHeader(v.MessageSearch.Status)
	}
	_, _ = buf.WriteTo(w)
}

func preference(r *http.Request, key string, choices []string, fallback string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		if c, err := r.Cookie("oie_" + key); err == nil {
			value = c.Value
		}
	}
	for _, choice := range choices {
		if choice == value {
			return value
		}
	}
	return fallback
}

func locale(r *http.Request) string {
	choice := preference(r, "lang", []string{"en", "pt-BR"}, "")
	if choice != "" {
		return choice
	}
	tags, _, err := language.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
	if err != nil {
		return "en"
	}
	_, index, _ := language.NewMatcher([]language.Tag{language.English, language.BrazilianPortuguese}).Match(tags...)
	return []string{"en", "pt-BR"}[index]
}

func savePreference(w http.ResponseWriter, r *http.Request, key, value string) {
	if r.URL.Query().Has(key) {
		http.SetCookie(w, &http.Cookie{
			Name: "oie_" + key, Value: value, Path: "/", MaxAge: 365 * 24 * 60 * 60,
			HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
		})
	}
}

func presentationContext(ctx context.Context, v view) context.Context {
	return expressions.With(ctx, expressions.Set{AppShell: expressions.AppShell{SkipLinkLabel: v.Text.Skip},
		Sidebar:   expressions.Sidebar{AriaLabel: v.Text.Navigation, ActiveLabel: v.Text.Active, TriggerLabel: v.Text.OpenNavigation, SkipLabel: v.Text.Skip},
		Drawer:    expressions.Drawer{CloseAriaLabel: v.Text.Close},
		TextInput: expressions.TextInput{SearchAriaLabel: v.Text.SearchChats, ShowPasswordAriaLabel: v.Text.ShowPassword},
		Select:    expressions.Select{Placeholder: v.Text.ChooseOption, ListAriaLabel: func(label string) string { return label }},
		Combobox:  expressions.Combobox{Placeholder: v.Text.ChooseOption, SearchPlaceholder: v.Text.SearchAccounts, EmptyText: v.Text.NoAccountsFound, ErrorText: v.Text.UnavailableBody, RetryLabel: v.Text.Retry},
		Form:      expressions.Form{ErrorTitle: v.Text.ValidationFailed, EditLabel: v.Text.Edit, DoneLabel: v.Text.Done},
	})
}

// accountUnread composes the existing paginated API without depending on the
// currently selected account, search or archive filter. Incomplete totals are
// hidden rather than presenting a partial count as an exact total.
func (s *Server) accountUnread(ctx context.Context) map[string]int {
	totals := map[string]int{}
	seen := map[string]bool{}
	limit := 100
	unread := true
	for _, archived := range []bool{false, true} {
		params := &engineapi.ListConversationsParams{Limit: &limit, UnreadOnly: &unread, Archived: &archived}
		cursors := map[string]bool{}
		for {
			response, err := s.client.ListConversationsWithResponse(ctx, params)
			if err != nil || response.JSON200 == nil {
				return nil
			}
			for _, c := range response.JSON200.Items {
				if !seen[c.Id] {
					totals[c.AccountId] += c.UnreadCount
					seen[c.Id] = true
				}
			}
			next := response.JSON200.NextCursor
			if next == nil || *next == "" {
				break
			}
			if cursors[*next] {
				return nil
			}
			cursors[*next] = true
			params.Cursor = next
		}
	}
	return totals
}
func (v view) activeAccountName() string {
	if v.AccountFilter != "" {
		if a := v.account(v.AccountFilter); a.Id != "" {
			return a.Label
		}
	}
	return v.Text.AllAccounts
}
func (v view) activeAccountAddress() string {
	if v.AccountFilter != "" {
		return v.account(v.AccountFilter).Address
	}
	return v.Text.SwitchAccount
}

func (v view) historyCaption(id string) string {
	h, ok := v.Histories[id]
	if !ok {
		return ""
	}
	label := ""
	switch h.State {
	case "syncing":
		label = v.Text.HistorySyncing
	case "partial", "error":
		label = v.Text.HistoryPartial
	case "unsupported":
		label = v.Text.HistoryUnsupported
	case "complete":
		label = v.Text.HistoryComplete
	default:
		return ""
	}
	return label + " · " + fmt.Sprintf(v.Text.HistoryCount, h.RetainedMessages)
}

//go:embed video-thumbnails.js
var videoThumbnailsScript []byte

//go:embed cache.js
var cacheScript []byte

//go:embed cache_vendor/lru-cache.min.mjs
var cacheLibrary []byte

//go:embed cache_vendor/LICENSE.md
var cacheLibraryLicense []byte
