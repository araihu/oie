package xmpp

import (
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
)

func defaultRoomNickname(account domain.Account) string {
	address, err := jid.Parse(account.Address)
	if err != nil {
		return ""
	}
	nickname := []rune(address.Localpart())
	if len(nickname) > 100 {
		nickname = nickname[:100]
	}
	return string(nickname)
}
