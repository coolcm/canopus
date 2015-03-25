package goap

import (
	"bytes"
	"encoding/binary"
	"strings"
	"log"
	"strconv"
)

func NewMessage(messageType uint8, messageCode CoapCode, messageId uint16) *Message {
	msg := &Message{}
	msg.MessageType = messageType
	msg.Code = messageCode

	return msg
}

func NewEmptyMessage(id uint16) *Message {
	msg := NewMessageOfType(TYPE_ACKNOWLEDGEMENT, id)

	return msg
}

func NewMessageOfType(t uint8, id uint16) *Message {
	return &Message{
		MessageType: t,
		MessageId:   id,
	}
}

/*
     0                   1                   2                   3
    0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
   |Ver| T |  TKL  |      Code     |          Message ID           |
   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
   |   Token (if any, TKL bytes) ...
   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
   |   Options (if any) ...
   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
   |1 1 1 1 1 1 1 1|    Payload (if any) ...
   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
*/
/**
Converts bytes to a CoAP Message
*/
func BytesToMessage(data []byte) (*Message, error) {
	msg := &Message{}

	dataLen := len(data)
	if dataLen < 4 {
		return msg, ERR_PACKET_LENGTH_LESS_THAN_4
	}

	ver := data[DATA_HEADER] >> 6
	if ver != 1 {
		return nil, ERR_INVALID_VERSION
	}

	msg.MessageType = data[DATA_HEADER] >> 4 & 0x03
	tokenLength := data[DATA_HEADER] & 0x0f
	msg.Code = CoapCode(data[DATA_CODE])

	msg.MessageId = binary.BigEndian.Uint16(data[DATA_MSGID_START:DATA_MSGID_END])

	// Token
	if tokenLength > 0 {
		msg.Token = make([]byte, tokenLength)
		token := data[DATA_TOKEN_START : DATA_TOKEN_START+tokenLength]
		copy(msg.Token, token)
	}

	/*
	    0   1   2   3   4   5   6   7
	   +---------------+---------------+
	   |               |               |
	   |  Option Delta | Option Length |   1 byte
	   |               |               |
	   +---------------+---------------+
	   \                               \
	   /         Option Delta          /   0-2 bytes
	   \          (extended)           \
	   +-------------------------------+
	   \                               \
	   /         Option Length         /   0-2 bytes
	   \          (extended)           \
	   +-------------------------------+
	   \                               \
	   /                               /
	   \                               \
	   /         Option Value          /   0 or more bytes
	   \                               \
	   /                               /
	   \                               \
	   +-------------------------------+
	*/
	tmp := data[DATA_TOKEN_START+msg.GetTokenLength():]

	lastOptionId := 0
	for len(tmp) > 0 {
		if tmp[0] == PAYLOAD_MARKER {
			tmp = tmp[1:]
			break
		}

		optionDelta := int(tmp[0] >> 4)
		optionLength := int(tmp[0] & 0x0f)

		tmp = tmp[1:]
		switch optionDelta {
		case 13:
			optionDeltaExtended := int(tmp[0])
			optionDelta += optionDeltaExtended
			tmp = tmp[1:]
			break

		case 14:
			optionDeltaExtended := decodeInt(tmp[:1])
			optionDelta += int(optionDeltaExtended - uint32(269))
			tmp = tmp[2:]
			break

		case 15:
			return msg, ERR_OPTION_DELTA_USES_VALUE_15
		}
		lastOptionId += optionDelta

		switch optionLength {
		case 13:
			optionLengthExtended := int(tmp[0])
			optionLength += optionLengthExtended
			tmp = tmp[1:]
			break

		case 14:
			optionLengthExtended := decodeInt(tmp[:1])
			optionLength += int(optionLengthExtended - uint32(269))
			tmp = tmp[2:]
			break

		case 15:
			return msg, ERR_OPTION_LENGTH_USES_VALUE_15
		}

		if optionLength > 0 {
			optionValue := tmp[:optionLength]

			optCode := OptionCode(lastOptionId)

			switch optCode {
			case OPTION_URI_PORT, OPTION_CONTENT_FORMAT, OPTION_MAX_AGE, OPTION_ACCEPT, OPTION_SIZE1,
			OPTION_BLOCK1, OPTION_BLOCK2:
				msg.Options = append(msg.Options, NewOption(optCode, decodeInt(optionValue)))
				break

			case OPTION_URI_HOST, OPTION_LOCATION_PATH, OPTION_URI_PATH, OPTION_URI_QUERY,
			OPTION_LOCATION_QUERY, OPTION_PROXY_URI, OPTION_PROXY_SCHEME:
				msg.Options = append(msg.Options, NewOption(optCode, string(optionValue)))
				break

			default:
				if lastOptionId &0x01 == 1 {
					log.Println("Unknown Critical Option id " + strconv.Itoa(lastOptionId))
					return msg, ERR_UNKNOWN_CRITICAL_OPTION
				}
				break
			}
			tmp = tmp[optionLength:]
		}
	}
	msg.Payload = tmp
	err := ValidateMessage(msg)

	return msg, err
}

func MessageToBytes(msg *Message) ([]byte, error) {
	messageId := []byte{0, 0}
	binary.BigEndian.PutUint16(messageId, msg.MessageId)

	buf := bytes.Buffer{}
	buf.Write([]byte{(1 << 6) | (msg.MessageType << 4) | 0x0f&msg.GetTokenLength()})
	buf.Write([]byte{byte(msg.Code)})
	buf.Write([]byte{messageId[0]})
	buf.Write([]byte{messageId[1]})
	buf.Write(msg.Token)

	lastOptionId := 0
	for _, opt := range msg.Options {
		b := valueToBytes(opt.Value)
		optCode := opt.Code
		bLen := len(b)
		if bLen >= 15 {
			buf.Write([]byte{byte(int(optCode)-lastOptionId)<<4 | 15, byte(bLen - 15)})
		} else {
			buf.Write([]byte{byte(int(optCode)-lastOptionId)<<4 | byte(bLen)})
		}

		if int(opt.Code)-lastOptionId > 15 {
			return nil, ERR_UNKNOWN_CRITICAL_OPTION
		}

		buf.Write(b)
		lastOptionId = int(opt.Code)
	}

	if len(msg.Payload) > 0 {
		buf.Write([]byte{PAYLOAD_MARKER})
	}

	buf.Write(msg.Payload)

	return buf.Bytes(), nil
}

func ValidateMessage(msg *Message) error {
	if msg.MessageType > 3 {
		return ERR_UNKNOWN_MESSAGE_TYPE
	}

	if msg.GetTokenLength() > 8 {
		return ERR_INVALID_TOKEN_LENGTH
	}

	// Repeated Unrecognized Options
	for _, opt := range msg.Options {
		opts := msg.GetOptions(opt.Code)

		if len(opts) > 1 {
			if !RepeatableOption(opts[0]) {
				if opts[0].Code&0x01 == 1 {
					return ERR_UNKNOWN_CRITICAL_OPTION
				}
			}
		}
	}

	return nil
}

type Message struct {
	MessageType uint8
	Code        CoapCode
	MessageId   uint16
	Payload     []byte
	Token       []byte
	Options     []*Option
}

func (c Message) GetCodeString() string {
	codeClass := string(c.Code >> 5)
	codeDetail := string(c.Code & 0x1f)

	return codeClass + "." + codeDetail
}

func (c Message) GetMethod() uint8 {
	return (byte(c.Code) & 0x1f)
}

func (c Message) GetTokenLength() uint8 {
	return uint8(len(c.Token))
}

func (c Message) GetOptions(id OptionCode) []*Option {
	var opts []*Option
	for _, val := range c.Options {
		if val.Code == id {
			opts = append(opts, val)
		}
	}
	return opts
}

func (c Message) GetOption(id OptionCode) *Option {
	for _, val := range c.Options {
		if val.Code == id {
			return val
		}
	}
	return nil
}

func (c Message) GetOptionsAsString(id OptionCode) []string {
	opts := c.GetOptions(id)

	var str []string
	for _, o := range opts {
		str = append(str, o.Value.(string))
	}
	return str
}

func (c Message) GetPath() string {
	opts := c.GetOptionsAsString(OPTION_URI_PATH)

    log.Println(opts)

	return strings.Join(opts, "/")
}

func (c *Message) MethodString() string {

	switch c.Code {
	case GET:
		return "GET"
		break

	case DELETE:
		return "DELETE"
		break

	case POST:
		return "POST"
		break

	case PUT:
		return "PUT"
		break
	}
	return ""
}

func (m *Message) AddOption(code OptionCode, value interface{}) {
	m.Options = append(m.Options, NewOption(code, value))
}

func (m *Message) AddOptions(opts []*Option) {
	for _, opt := range opts {
		m.Options = append(m.Options, opt)
	}
}

func (m *Message) CloneOptions(cm *Message, opts ...OptionCode) {
	for _, opt := range opts {
		m.AddOptions(cm.GetOptions(opt))
	}
}

func (m *Message) RemoveOptions(id OptionCode) {
	var opts []*Option
	for _, opt := range m.Options {
		if opt.Code != id {
			opts = append(opts, opt)
		}
	}
	m.Options = opts
}

func (m *Message) SetStringPayload(s string) {
	m.Payload = []byte(s)
}

/* Helpers */
func valueToBytes(value interface{}) []byte {
	var v uint32

	switch i := value.(type) {
	case string:
		return []byte(i)
	case []byte:
		return i
	case MediaType:
		v = uint32(i)
	case byte:
		v = uint32(i)
	case int:
		v = uint32(i)
	case int32:
		v = uint32(i)
	case uint:
		v = uint32(i)
	case uint32:
		v = i
	default:
		break
	}

	return encodeInt(v)
}

func PayloadAsString(b []byte) string {
	buff := bytes.NewBuffer(b)

	return buff.String()
}

func decodeInt(b []byte) uint32 {
	tmp := []byte{0, 0, 0, 0}
	copy(tmp[4-len(b):], b)

	return binary.BigEndian.Uint32(tmp)
}

func encodeInt(v uint32) []byte {
	switch {
	case v == 0:
		return nil

	case v < 256:
		return []byte{byte(v)}

	case v < 65536:
		rv := []byte{0, 0}
		binary.BigEndian.PutUint16(rv, uint16(v))
		return rv

	case v < 16777216:
		rv := []byte{0, 0, 0, 0}
		binary.BigEndian.PutUint32(rv, uint32(v))
		return rv[1:]

	default:
		rv := []byte{0, 0, 0, 0}
		binary.BigEndian.PutUint32(rv, uint32(v))
		return rv
	}
}
