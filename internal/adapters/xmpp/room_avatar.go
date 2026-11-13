package xmpp

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"strings"
)

const roomVCardQuery = `<vCard xmlns="vcard-temp"/>`

// Update just the photo. Room vCards can also hold published names and
// descriptions, which must survive replacing or removing the avatar.
func updateRoomVCard(payload string, image []byte) (string, error) {
	var card struct {
		Cards []struct {
			Fields string `xml:",innerxml"`
		} `xml:"vcard-temp vCard"`
	}
	var fields string
	if strings.TrimSpace(payload) != "" {
		if xml.Unmarshal([]byte("<result>"+payload+"</result>"), &card) != nil || len(card.Cards) != 1 {
			return "", errArchive
		}
		fields = card.Cards[0].Fields
	}
	var result strings.Builder
	result.WriteString(`<vCard xmlns="vcard-temp">`)
	decoder := xml.NewDecoder(strings.NewReader(fields))
	for {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		element, isElement := token.(xml.StartElement)
		if isElement {
			if err := decoder.Skip(); err != nil {
				return "", err
			}
			if element.Name.Local == "PHOTO" && (element.Name.Space == "" || element.Name.Space == "vcard-temp") {
				continue
			}
		}
		result.WriteString(fields[start:decoder.InputOffset()])
	}
	if len(image) > 0 {
		result.WriteString(`<PHOTO><TYPE>image/png</TYPE><BINVAL>`)
		result.WriteString(base64.StdEncoding.EncodeToString(image))
		result.WriteString(`</BINVAL></PHOTO>`)
	}
	result.WriteString(`</vCard>`)
	return result.String(), nil
}
