package remoteblob

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"hash"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/pkg/sftp"
	"github.com/pocketbase/pocketbase/tools/filesystem/blob"
	"golang.org/x/crypto/ssh"
)

// Options defines the configuration for the remote driver.
type Options struct {
	Host     string
	Port     int
	User     string
	Password string
	Type     string // "ftp" or "sftp"
}

// New creates a new generic remote driver (FTP or SFTP).
func New(opts Options) (blob.Driver, error) {
	if opts.Type == "sftp" {
		return newSFTP(opts)
	}
	if opts.Type == "ftp" {
		return newFTP(opts)
	}
	return nil, fmt.Errorf("unsupported connection type: %s", opts.Type)
}

// clientInterface abstracts the common operations between FTP and SFTP.
type clientInterface interface {
	Stat(path string) (int64, time.Time, bool, error) // size, modTime, isDir, err
	Open(path string) (io.ReadCloser, error)
	Create(path string) (io.WriteCloser, error)
	Delete(path string) error
	Rename(oldPath, newPath string) error
	List(path string) ([]*blob.ListObject, error)
	Close() error
}

type driver struct {
	client clientInterface
	mu     sync.Mutex
}

func (d *driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		return d.client.Close()
	}
	return nil
}

func (d *driver) NormalizeError(err error) error {
	if errors.Is(err, blob.ErrNotFound) {
		return err
	}
	// Basic normalization; specialized implementations could do more
	if strings.Contains(err.Error(), "file does not exist") || strings.Contains(err.Error(), "not found") {
		return errors.Join(err, blob.ErrNotFound)
	}
	return err
}

// Attributes reads the file stats and the sidecar .attrs file.
func (d *driver) Attributes(ctx context.Context, key string) (*blob.Attributes, error) {
	// Check if main file exists
	size, modTime, isDir, err := d.client.Stat(key)
	if err != nil {
		return nil, d.NormalizeError(err)
	}
	if isDir {
		return nil, blob.ErrNotFound
	}

	attrs := &blob.Attributes{
		ModTime: modTime,
		Size:    size,
		ETag:    fmt.Sprintf("\"%x-%x\"", modTime.UnixNano(), size),
	}

	// Try to read sidecar .attrs file
	r, err := d.client.Open(key + attrsExt)
	if err == nil {
		defer r.Close()
		data, _ := io.ReadAll(r)
		if xa, err := decodeAttrs(data); err == nil {
			attrs.ContentType = xa.ContentType
			attrs.ContentDisposition = xa.ContentDisposition
			attrs.ContentEncoding = xa.ContentEncoding
			attrs.ContentLanguage = xa.ContentLanguage
			attrs.CacheControl = xa.CacheControl
			attrs.Metadata = xa.Metadata
			attrs.MD5 = xa.MD5
		}
	}

	if attrs.ContentType == "" {
		attrs.ContentType = "application/octet-stream"
	}

	return attrs, nil
}

func (d *driver) NewRangeReader(ctx context.Context, key string, offset, length int64) (blob.DriverReader, error) {
	// Note: Efficient range reading depends on the underlying protocol.
	// For simplicity in this wrapper, we open the file and seek/limit.
	// Ideally, the clientInterface.Open should support offsets if possible.
	rc, err := d.client.Open(key)
	if err != nil {
		return nil, d.NormalizeError(err)
	}

	// If offset is required, we read and discard (inefficient but generic).
	// Optimization: Implement specific RangeOpen in clientInterface if needed.
	if offset > 0 {
		if seeker, ok := rc.(io.Seeker); ok {
			_, err = seeker.Seek(offset, io.SeekStart)
		} else {
			_, err = io.CopyN(io.Discard, rc, offset)
		}
		if err != nil {
			rc.Close()
			return nil, err
		}
	}

	var r io.Reader = rc
	if length >= 0 {
		r = io.LimitReader(rc, length)
	}

	// We need attributes for the Reader
	attrs, err := d.Attributes(ctx, key)
	if err != nil {
		rc.Close()
		return nil, err
	}

	return &reader{
		rc: rc,
		r:  r,
		attrs: &blob.ReaderAttributes{
			ContentType: attrs.ContentType,
			ModTime:     attrs.ModTime,
			Size:        attrs.Size,
		},
	}, nil
}

type reader struct {
	rc    io.ReadCloser
	r     io.Reader
	attrs *blob.ReaderAttributes
}

func (r *reader) Read(p []byte) (n int, err error)   { return r.r.Read(p) }
func (r *reader) Close() error                       { return r.rc.Close() }
func (r *reader) Attributes() *blob.ReaderAttributes { return r.attrs }

func (d *driver) NewTypedWriter(ctx context.Context, key, contentType string, opts *blob.WriterOptions) (blob.DriverWriter, error) {
	// We write to a temporary file first (key + ".part") to allow for atomicity via rename
	tempKey := key + ".part"

	w, err := d.client.Create(tempKey)
	if err != nil {
		return nil, err
	}

	return &writer{
		d:           d,
		w:           w,
		key:         key,
		tempKey:     tempKey,
		contentType: contentType,
		opts:        opts,
		md5hash:     md5.New(),
	}, nil
}

type writer struct {
	d           *driver
	w           io.WriteCloser
	key         string
	tempKey     string
	contentType string
	opts        *blob.WriterOptions
	md5hash     hash.Hash
}

func (w *writer) Write(p []byte) (n int, err error) {
	n, err = w.w.Write(p)
	if n > 0 {
		w.md5hash.Write(p[:n])
	}
	return n, err
}

func (w *writer) Close() error {
	// Close the temp file upload
	if err := w.w.Close(); err != nil {
		return err
	}

	// Write attributes to sidecar
	xa := xattrs{
		ContentType:        w.contentType,
		ContentDisposition: w.opts.ContentDisposition,
		CacheControl:       w.opts.CacheControl,
		ContentLanguage:    w.opts.ContentLanguage,
		ContentEncoding:    w.opts.ContentEncoding,
		Metadata:           w.opts.Metadata,
		MD5:                w.md5hash.Sum(nil),
		ModTime:            time.Now(),
	}

	attrBytes, _ := xa.Bytes()

	// Write .attrs file
	aw, err := w.d.client.Create(w.key + attrsExt)
	if err != nil {
		return err
	}
	if _, err := aw.Write(attrBytes); err != nil {
		aw.Close()
		return err
	}
	if err := aw.Close(); err != nil {
		return err
	}

	// Rename .part to actual key
	return w.d.client.Rename(w.tempKey, w.key)
}

func (d *driver) Copy(ctx context.Context, dstKey, srcKey string) error {
	// Reading and writing through the stream to support copying
	r, err := d.NewRangeReader(ctx, srcKey, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()

	// Get original attributes
	attrs, err := d.Attributes(ctx, srcKey)
	if err != nil {
		return err
	}

	opts := &blob.WriterOptions{
		ContentType:        attrs.ContentType,
		ContentDisposition: attrs.ContentDisposition,
		CacheControl:       attrs.CacheControl,
		ContentLanguage:    attrs.ContentLanguage,
		ContentEncoding:    attrs.ContentEncoding,
		Metadata:           attrs.Metadata,
	}

	w, err := d.NewTypedWriter(ctx, dstKey, attrs.ContentType, opts)
	if err != nil {
		return err
	}

	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}

	return w.Close()
}

func (d *driver) Delete(ctx context.Context, key string) error {
	_ = d.client.Delete(key + attrsExt) // Ignore error on attrs delete
	return d.client.Delete(key)
}

func (d *driver) ListPaged(ctx context.Context, opts *blob.ListOptions) (*blob.ListPage, error) {
	// Naive implementation: List directory, filter in memory.
	// For deep prefixes (a/b/c), we assume the client.List handles the directory walk or we map keys.

	// If delimiter is empty, we treat as flat (recursive).
	// If delimiter is "/", we scan only the current directory level.

	// NOTE: Implementing efficient recursive list on FTP/SFTP is slow.
	// We assume the Prefix points to a directory or file.

	dir := path.Dir(opts.Prefix)
	if dir == "." {
		dir = ""
	}

	objects, err := d.client.List(dir)
	if err != nil {
		return nil, err
	}

	// Filter and pagination
	filtered := make([]*blob.ListObject, 0)
	for _, obj := range objects {
		if strings.HasPrefix(obj.Key, opts.Prefix) {
			// Handle delimiter (virtual directories)
			if opts.Delimiter != "" {
				// Check if key contains delimiter after prefix
				rest := strings.TrimPrefix(obj.Key, opts.Prefix)
				if idx := strings.Index(rest, opts.Delimiter); idx >= 0 {
					// It's a directory
					groupKey := opts.Prefix + rest[:idx+len(opts.Delimiter)]

					// Check if we already added this group
					found := false
					for _, existing := range filtered {
						if existing.Key == groupKey && existing.IsDir {
							found = true
							break
						}
					}
					if !found {
						filtered = append(filtered, &blob.ListObject{Key: groupKey, IsDir: true})
					}
					continue
				}
			}
			filtered = append(filtered, obj)
		}
	}

	// Sort
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Key < filtered[j].Key
	})

	// Pagination
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 1000
	}

	startIndex := 0
	if len(opts.PageToken) > 0 {
		token := string(opts.PageToken)
		// Find start index
		for i, obj := range filtered {
			if obj.Key > token {
				startIndex = i
				break
			}
		}
		if startIndex == 0 && filtered[0].Key <= token {
			// If no key is greater, we are done
			return &blob.ListPage{}, nil
		}
	}

	endIndex := startIndex + pageSize
	if endIndex > len(filtered) {
		endIndex = len(filtered)
	}

	result := filtered[startIndex:endIndex]

	var nextPageToken []byte
	if endIndex < len(filtered) {
		nextPageToken = []byte(result[len(result)-1].Key)
	}

	return &blob.ListPage{
		Objects:       result,
		NextPageToken: nextPageToken,
	}, nil
}

// -------------------------------------------------------------------
// SFTP Implementation
// -------------------------------------------------------------------

type sftpClient struct {
	c    *sftp.Client
	conn *ssh.Client
}

func newSFTP(opts Options) (*driver, error) {
	config := &ssh.ClientConfig{
		User: opts.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(opts.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Warning: Insecure, for production use known hosts
		Timeout:         10 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, err
	}

	c, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return &driver{client: &sftpClient{c: c, conn: conn}}, nil
}

func (s *sftpClient) Close() error {
	s.c.Close()
	return s.conn.Close()
}

func (s *sftpClient) Stat(path string) (int64, time.Time, bool, error) {
	fi, err := s.c.Stat(path)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	return fi.Size(), fi.ModTime(), fi.IsDir(), nil
}

func (s *sftpClient) Open(path string) (io.ReadCloser, error) {
	return s.c.Open(path)
}

func (s *sftpClient) Create(path string) (io.WriteCloser, error) {
	// Ensure dir exists
	dir := path
	if idx := strings.LastIndex(path, "/"); idx != -1 {
		dir = path[:idx]
		s.c.MkdirAll(dir)
	}
	return s.c.Create(path)
}

func (s *sftpClient) Delete(path string) error {
	return s.c.Remove(path)
}

func (s *sftpClient) Rename(oldPath, newPath string) error {
	return s.c.Rename(oldPath, newPath)
}

func (s *sftpClient) List(root string) ([]*blob.ListObject, error) {
	if root == "" {
		root = "."
	}
	walker := s.c.Walk(root)
	var objs []*blob.ListObject
	for walker.Step() {
		if walker.Err() != nil {
			continue
		}
		path := walker.Path()
		stat := walker.Stat()
		if stat.IsDir() {
			continue
		}
		// normalize key (remove leading ./)
		key := strings.TrimPrefix(path, "./")
		key = strings.TrimPrefix(key, "/")

		objs = append(objs, &blob.ListObject{
			Key:     key,
			ModTime: stat.ModTime(),
			Size:    stat.Size(),
		})
	}
	return objs, nil
}

// -------------------------------------------------------------------
// FTP Implementation
// -------------------------------------------------------------------

type ftpClient struct {
	c *ftp.ServerConn
}

func newFTP(opts Options) (*driver, error) {
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	c, err := ftp.Dial(addr, ftp.DialWithTimeout(10*time.Second))
	if err != nil {
		return nil, err
	}

	if err := c.Login(opts.User, opts.Password); err != nil {
		c.Quit()
		return nil, err
	}

	return &driver{client: &ftpClient{c: c}}, nil
}

func (f *ftpClient) Close() error {
	return f.c.Quit()
}

func (f *ftpClient) Stat(path string) (int64, time.Time, bool, error) {
	// FTP Stat is tricky; using List is often more reliable
	entry, err := f.c.List(path)
	if err == nil && len(entry) == 1 {
		return int64(entry[0].Size), entry[0].Time, entry[0].Type == ftp.EntryTypeFolder, nil
	}

	// Fallback attempts
	size, err := f.c.FileSize(path)
	if err != nil {
		return 0, time.Time{}, false, blob.ErrNotFound
	}
	t, _ := f.c.GetTime(path)
	return size, t, false, nil
}

func (f *ftpClient) Open(path string) (io.ReadCloser, error) {
	return f.c.Retr(path)
}

func (f *ftpClient) Create(path string) (io.WriteCloser, error) {
	// FTP doesn't expose a simple WriteCloser for streaming upload easily in all libs without pipe
	// jlaffaye/ftp Stor takes a reader. We need a pipe.
	r, w := io.Pipe()

	go func() {
		err := f.c.Stor(path, r)
		r.CloseWithError(err)
	}()

	return w, nil
}

func (f *ftpClient) Delete(path string) error {
	return f.c.Delete(path)
}

func (f *ftpClient) Rename(oldPath, newPath string) error {
	return f.c.Rename(oldPath, newPath)
}

func (f *ftpClient) List(root string) ([]*blob.ListObject, error) {
	// Basic implementation listing current dir recursively?
	// FTP recursive list is hard. Walker logic similar to SFTP is needed but manual.
	// For brevity, this implements a flat list or needs a proper walker function.
	// Here we return empty for safety or implement basic walker if critical.
	return nil, errors.New("FTP recursive list not fully implemented in this snippet")
}
