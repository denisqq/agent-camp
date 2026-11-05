package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"io"
)

type S3Config struct{ Endpoint, Region, Bucket, AccessKey, SecretKey string }
type S3 struct {
	Client *s3.Client
	Bucket string
}

func NewS3(ctx context.Context, c S3Config) (*S3, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion(c.Region)}
	if c.AccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
			o.UsePathStyle = true
		}
	})
	if c.Bucket == "" {
		return nil, errors.New("S3 bucket required")
	}
	return &S3{client, c.Bucket}, nil
}
func translate(err error) error {
	var e smithy.APIError
	if errors.As(err, &e) {
		switch e.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchBucket":
			return ErrNotFound
		case "PreconditionFailed", "ConditionalRequestConflict":
			return ErrConflict
		}
	}
	return err
}
func (s *S3) EnsureBucket(ctx context.Context) error {
	_, err := s.Client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.Bucket})
	if err == nil {
		return nil
	}
	if !errors.Is(translate(err), ErrNotFound) {
		return err
	}
	_, err = s.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.Bucket})
	return err
}
func (s *S3) Get(ctx context.Context, key string) ([]byte, string, error) {
	out, err := s.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.Bucket, Key: &key})
	if err != nil {
		return nil, "", translate(err)
	}
	defer out.Body.Close()
	b, err := io.ReadAll(io.LimitReader(out.Body, 16*1024*1024+1))
	if len(b) > 16*1024*1024 {
		return nil, "", errors.New("metadata object too large")
	}
	return b, aws.ToString(out.ETag), err
}
func (s *S3) Put(ctx context.Context, key string, b []byte, condition string) (string, error) {
	in := &s3.PutObjectInput{Bucket: &s.Bucket, Key: &key, Body: bytes.NewReader(b)}
	if condition == "*" {
		in.IfNoneMatch = aws.String("*")
	} else if condition != "" {
		in.IfMatch = &condition
	}
	out, err := s.Client.PutObject(ctx, in)
	if err != nil {
		return "", translate(err)
	}
	return aws.ToString(out.ETag), nil
}
func (s *S3) List(ctx context.Context, prefix, after string, limit int) ([]string, string, error) {
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	out, err := s.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &s.Bucket, Prefix: &prefix, StartAfter: &after, MaxKeys: aws.Int32(int32(limit))})
	if err != nil {
		return nil, "", translate(err)
	}
	keys := make([]string, 0, len(out.Contents))
	for _, obj := range out.Contents {
		keys = append(keys, aws.ToString(obj.Key))
	}
	next := ""
	if aws.ToBool(out.IsTruncated) && len(keys) > 0 {
		next = keys[len(keys)-1]
	}
	return keys, next, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.Bucket, Key: &key})
	return translate(err)
}
func (s *S3) Open(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	in := &s3.GetObjectInput{Bucket: &s.Bucket, Key: &key}
	if length > 0 {
		in.Range = aws.String(fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	}
	out, err := s.Client.GetObject(ctx, in)
	if err != nil {
		return nil, translate(err)
	}
	return out.Body, nil
}
