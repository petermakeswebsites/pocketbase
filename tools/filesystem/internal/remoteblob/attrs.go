package remoteblob

import (
	"encoding/json"
	"time"
)

const attrsExt = ".attrs"

// xattrs stores extended attributes for an object.
type xattrs struct {
	CacheControl       string            `json:"user.cache_control"`
	ContentDisposition string            `json:"user.content_disposition"`
	ContentEncoding    string            `json:"user.content_encoding"`
	ContentLanguage    string            `json:"user.content_language"`
	ContentType        string            `json:"user.content_type"`
	Metadata           map[string]string `json:"user.metadata"`
	MD5                []byte            `json:"md5"`
	ModTime            time.Time         `json:"mod_time"` // Stored explicitly for consistency
	Size               int64             `json:"size"`
}

func (xa *xattrs) Bytes() ([]byte, error) {
	return json.Marshal(xa)
}

func decodeAttrs(data []byte) (*xattrs, error) {
	xa := new(xattrs)
	if err := json.Unmarshal(data, xa); err != nil {
		return nil, err
	}
	return xa, nil
}
