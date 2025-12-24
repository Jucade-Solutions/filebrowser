package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3Storage struct {
	Client *s3.Client
	Bucket string
	Prefix string
	Root   string
}

type s3FileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

func (f *s3FileInfo) Name() string       { return f.name }
func (f *s3FileInfo) Size() int64        { return f.size }
func (f *s3FileInfo) Mode() os.FileMode {
	if f.isDir {
		return os.ModeDir | 0755
	}
	return 0644
}
func (f *s3FileInfo) ModTime() time.Time { return f.modTime }
func (f *s3FileInfo) IsDir() bool        { return f.isDir }
func (f *s3FileInfo) Sys() interface{}   { return nil }

// s3FileInfoFixed is no longer needed if we fix s3FileInfo directly, but I'll keep it for consistency with previous changes if needed or just remove it.
// Actually, let's just make s3FileInfo implement FileInfo correctly.

func NewS3Storage(bucket, region, endpoint, accessKey, secretKey, prefix, root string) (Storage, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		if endpoint != "" {
			return aws.Endpoint{
				URL:           endpoint,
				SigningRegion: region,
			}, nil
		}
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		config.WithEndpointResolverWithOptions(customResolver),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	return &S3Storage{
		Client: client,
		Bucket: bucket,
		Prefix: prefix,
		Root:   root,
	}, nil
}

func (s *S3Storage) Open(path string) (File, error) {
	key := s.getKey(path)
	if key == "" || key == "." {
		return &s3File{storage: s, path: path, isDir: true}, nil
	}

	// Try GetObject first (for files)
	output, err := s.Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		isDir := strings.HasSuffix(key, "/")
		return &s3File{ReadCloser: output.Body, storage: s, path: path, isDir: isDir}, nil
	}

	// If it fails, check if it's a directory
	if s.isDir(key) {
		return &s3File{storage: s, path: path, isDir: true}, nil
	}

	return nil, err
}

func (s *S3Storage) getKey(path string) string {
	// Filebrowser might pass double-encoded paths
	for {
		unescaped, err := url.QueryUnescape(path)
		if err != nil || unescaped == path {
			break
		}
		path = unescaped
	}

	if s.Root != "" {
		path = strings.TrimPrefix(path, s.Root)
	}
	path = strings.TrimLeft(path, "/")
	path = strings.TrimSpace(path)

	key := path
	if s.Prefix != "" {
		key = filepath.Join(s.Prefix, path)
	}

	// Always use forward slashes for S3 keys
	key = filepath.ToSlash(key)
	fmt.Printf("DEBUG: S3 getKey path=%s key=%s\n", path, key)
	return key
}

func (s *S3Storage) Stat(path string) (os.FileInfo, error) {
	key := s.getKey(path)
	if key == "" || key == "." {
		return &s3FileInfo{name: "/", isDir: true}, nil
	}

	// Try HeadObject first
	output, err := s.Client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		isDir := strings.HasSuffix(key, "/")
		fmt.Printf("DEBUG: S3 Stat HeadObject OK: key=%s isDir=%v\n", key, isDir)
		return &s3FileInfo{
			name:    filepath.Base(path),
			size:    aws.ToInt64(output.ContentLength),
			modTime: *output.LastModified,
			isDir:   isDir,
		}, nil
	}
	fmt.Printf("DEBUG: S3 Stat HeadObject Failed: key=%s err=%v\n", key, err)

	// If metadata fails, check if it's a directory (might not have a marker object)
	if s.isDir(path) {
		fmt.Printf("DEBUG: S3 Stat isDir OK: path=%s\n", path)
		return &s3FileInfo{name: filepath.Base(path), isDir: true, modTime: time.Now()}, nil
	}

	return nil, err
}

func (s *S3Storage) isDir(path string) bool {
	key := s.getKey(path)
	if key == "" || key == "." {
		return true
	}
	prefix := key
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	output, err := s.Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.Bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(5),
	})
	isDir := err == nil && (len(output.Contents) > 0 || len(output.CommonPrefixes) > 0)
	fmt.Printf("DEBUG: S3 isDir prefix=%s result=%v contents=%d prefixes=%d\n", prefix, isDir, len(output.Contents), len(output.CommonPrefixes))
	return isDir
}

func (s *S3Storage) ReadDir(path string) ([]os.FileInfo, error) {
	prefix := s.getKey(path)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	output, err := s.Client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.Bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		return nil, err
	}

	var infos []os.FileInfo
	for _, p := range output.CommonPrefixes {
		name := strings.TrimPrefix(strings.TrimSuffix(*p.Prefix, "/"), prefix)
		if name == "" {
			continue
		}
		infos = append(infos, &s3FileInfo{name: name, isDir: true})
	}
	for _, obj := range output.Contents {
		name := strings.TrimPrefix(*obj.Key, prefix)
		if name == "" {
			continue
		}
		infos = append(infos, &s3FileInfo{
			name:    name,
			size:    aws.ToInt64(obj.Size),
			modTime: *obj.LastModified,
			isDir:   false,
		})
	}
	return infos, nil
}

func (s *S3Storage) MkdirAll(path string, perm os.FileMode) error {
	// S3 is flat, but we can simulate directories by creating a zero-byte object with a trailing slash
	key := s.getKey(path)
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	_, err := s.Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(""),
	})
	return err
}

func (s *S3Storage) RemoveAll(path string) error {
	key := s.getKey(path)
	// If it's a directory, we need to delete all objects with this prefix
	isDir := s.isDir(path)

	prefix := key
	if isDir && prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// List all objects with this prefix
	paginator := s3.NewListObjectsV2Paginator(s.Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.Bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return err
		}

		if len(page.Contents) == 0 {
			continue
		}

		var objects []types.ObjectIdentifier
		for _, obj := range page.Contents {
			objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
		}

		_, err = s.Client.DeleteObjects(context.TODO(), &s3.DeleteObjectsInput{
			Bucket: aws.String(s.Bucket),
			Delete: &types.Delete{Objects: objects},
		})
		if err != nil {
			return err
		}
	}

	// Also delete the object itself if it wasn't caught by the prefix (e.g. empty directory marker)
	_, err := s.Client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *S3Storage) OpenFile(path string, flag int, perm os.FileMode) (File, error) {
	// This is complex for S3 if flag is O_RDWR or O_APPEND
	// For Filebrowser, it mostly uses O_CREATE|O_TRUNC for writes
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		return &s3FileWriter{storage: s, path: path}, nil
	}
	return s.Open(path)
}

func (s *S3Storage) Exists(path string) bool {
	_, err := s.Stat(path)
	return err == nil
}

type s3File struct {
	io.ReadCloser
	storage     *S3Storage
	path        string
	isDir       bool
	localBuffer *os.File
}

func (f *s3File) Close() error {
	var err error
	if f.ReadCloser != nil {
		err = f.ReadCloser.Close()
	}
	if f.localBuffer != nil {
		f.localBuffer.Close()
		os.Remove(f.localBuffer.Name())
	}
	return err
}

func (f *s3File) Read(p []byte) (n int, err error) {
	if f.localBuffer != nil {
		return f.localBuffer.Read(p)
	}
	return f.ReadCloser.Read(p)
}

func (f *s3File) Write(p []byte) (n int, err error) { return 0, fmt.Errorf("read-only file") }

func (f *s3File) ensureLocalBuffer() error {
	if f.localBuffer != nil {
		return nil
	}
	// Download to temp file
	tmp, err := os.CreateTemp("", "s3read-*")
	if err != nil {
		return err
	}
	if f.ReadCloser != nil {
		_, err = io.Copy(tmp, f.ReadCloser)
		f.ReadCloser.Close()
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
	}
	_, err = tmp.Seek(0, 0)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	f.localBuffer = tmp
	return nil
}

func (f *s3File) Seek(offset int64, whence int) (int64, error) {
	if err := f.ensureLocalBuffer(); err != nil {
		return 0, err
	}
	return f.localBuffer.Seek(offset, whence)
}

func (f *s3File) ReadAt(p []byte, off int64) (n int, err error) {
	if err := f.ensureLocalBuffer(); err != nil {
		return 0, err
	}
	return f.localBuffer.ReadAt(p, off)
}
func (f *s3File) Stat() (os.FileInfo, error) { return f.storage.Stat(f.path) }
func (f *s3File) Readdir(count int) ([]os.FileInfo, error) {
	if !f.isDir {
		return nil, fmt.Errorf("not a directory")
	}
	return f.storage.ReadDir(f.path)
}

type s3FileWriter struct {
	storage *S3Storage
	path    string
	buffer  *os.File // Temporary file for buffering writes
}

func (f *s3FileWriter) Write(p []byte) (n int, err error) {
	if f.buffer == nil {
		f.buffer, err = os.CreateTemp("", "s3upload-*")
		if err != nil {
			return 0, err
		}
	}
	return f.buffer.Write(p)
}

func (f *s3FileWriter) Close() error {
	key := f.storage.getKey(f.path)
	if key == "" {
		return fmt.Errorf("invalid empty key for S3 object creation")
	}

	if f.buffer == nil {
		// Create empty file
		fmt.Printf("DEBUG: S3 PutObject (EMPTY) key=%s\n", key)
		_, err := f.storage.Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket:      aws.String(f.storage.Bucket),
			Key:         aws.String(key),
			Body:        strings.NewReader(""),
			ContentType: aws.String("application/octet-stream"),
		})
		return err
	}
	defer os.Remove(f.buffer.Name())
	defer f.buffer.Close()

	_, err := f.buffer.Seek(0, 0)
	if err != nil {
		return err
	}

	stat, err := f.buffer.Stat()
	if err != nil {
		return err
	}

	fmt.Printf("DEBUG: S3 PutObject key=%s size=%d\n", key, stat.Size())

	_, err = f.storage.Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:        aws.String(f.storage.Bucket),
		Key:           aws.String(key),
		Body:          f.buffer,
		ContentLength: aws.Int64(stat.Size()),
		ContentType:   aws.String("application/octet-stream"),
	})
	if err != nil {
		fmt.Printf("DEBUG: S3 PutObject Failed: %v\n", err)
	}
	return err
}

func (f *s3FileWriter) Read(p []byte) (n int, err error) { return 0, io.EOF }
func (f *s3FileWriter) Seek(offset int64, whence int) (int64, error) {
	if f.buffer == nil {
		if offset == 0 && whence == io.SeekStart {
			return 0, nil
		}
		return 0, fmt.Errorf("buffer not initialized")
	}
	return f.buffer.Seek(offset, whence)
}
func (f *s3FileWriter) Stat() (os.FileInfo, error) {
	if f.buffer == nil {
		return &s3FileInfo{name: filepath.Base(f.path), isDir: false}, nil
	}
	stat, err := f.buffer.Stat()
	if err != nil {
		return nil, err
	}
	// Use the original path's base name instead of the temp file name
	return &s3FileInfo{
		name:    filepath.Base(f.path),
		size:    stat.Size(),
		modTime: stat.ModTime(),
		isDir:   false,
	}, nil
}
func (f *s3FileWriter) Readdir(count int) ([]os.FileInfo, error) { return nil, nil }
