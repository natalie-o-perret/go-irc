package irc

// IRC command name constants.
const (
	// Connection registration
	PASS     = "PASS"
	NICK     = "NICK"
	USER     = "USER"
	OPER     = "OPER"
	QUIT     = "QUIT"
	SQUIT    = "SQUIT"

	// Channel operations
	JOIN    = "JOIN"
	PART    = "PART"
	MODE    = "MODE"
	TOPIC   = "TOPIC"
	NAMES   = "NAMES"
	LIST    = "LIST"
	INVITE  = "INVITE"
	KICK    = "KICK"

	// Messaging
	PRIVMSG = "PRIVMSG"
	NOTICE  = "NOTICE"
	TAGMSG  = "TAGMSG"

	// Server queries
	MOTD    = "MOTD"
	LUSERS  = "LUSERS"
	VERSION = "VERSION"
	STATS   = "STATS"
	LINKS   = "LINKS"
	TIME    = "TIME"
	CONNECT = "CONNECT"
	TRACE   = "TRACE"
	ADMIN   = "ADMIN"
	INFO    = "INFO"

	// User queries
	WHO    = "WHO"
	WHOIS  = "WHOIS"
	WHOWAS = "WHOWAS"

	// Misc
	KILL     = "KILL"
	PING     = "PING"
	PONG     = "PONG"
	ERROR    = "ERROR"
	AWAY     = "AWAY"
	REHASH   = "REHASH"
	RESTART  = "RESTART"
	SUMMON   = "SUMMON"
	USERS_   = "USERS"
	WALLOPS  = "WALLOPS"
	USERHOST = "USERHOST"
	ISON     = "ISON"

	// IRCv3
	CAP          = "CAP"
	AUTHENTICATE = "AUTHENTICATE"
	BATCH        = "BATCH"
	SETNAME      = "SETNAME"
	CHGHOST      = "CHGHOST"
	ACCOUNT      = "ACCOUNT"
	ACK          = "ACK"

	// DCC/CTCP
	CTCP_ACTION   = "ACTION"
	CTCP_VERSION  = "VERSION"
	CTCP_PING     = "PING"
	CTCP_TIME     = "TIME"
	CTCP_FINGER   = "FINGER"
	CTCP_DCC      = "DCC"
	CTCP_USERINFO = "USERINFO"
)

// CAP sub-commands.
const (
	CapLS    = "LS"
	CapLIST  = "LIST"
	CapREQ   = "REQ"
	CapACK   = "ACK"
	CapNAK   = "NAK"
	CapEND   = "END"
	CapNEW   = "NEW"
	CapDEL   = "DEL"
)

// IRCv3 capability names.
const (
	CapSASL              = "sasl"
	CapServerTime        = "server-time"
	CapMessageTags       = "message-tags"
	CapBatch             = "batch"
	CapLabeledResponse   = "labeled-response"
	CapEchoMessage       = "echo-message"
	CapMultiPrefix       = "multi-prefix"
	CapAwayNotify        = "away-notify"
	CapExtendedJoin      = "extended-join"
	CapChghost           = "chghost"
	CapSetname           = "setname"
	CapAccountTag        = "account-tag"
	CapCapNotify         = "cap-notify"
	CapChatHistory       = "draft/chathistory"
	CapBouncerNetworks   = "soju.im/bouncer-networks"
	CapBouncerNetworksNotify = "soju.im/bouncer-networks-notify"
	CapUserHostInNames   = "userhost-in-names"
	CapInviteNotify      = "invite-notify"
	CapAccountNotify     = "account-notify"
)

