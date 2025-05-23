package tenant

import (
	"context"
	"encoding/json"

	"github.com/influxdata/influxdb/v2"
	"github.com/influxdata/influxdb/v2/kit/platform"
	"github.com/influxdata/influxdb/v2/kit/platform/errors"
	"github.com/influxdata/influxdb/v2/kv"
	errors2 "github.com/influxdata/influxdb/v2/pkg/errors"
)

var (
	bucketBucket = []byte("bucketsv1")
	bucketIndex  = []byte("bucketindexv1")
)

func bucketIndexKey(o platform.ID, name string) ([]byte, error) {
	orgID, err := o.Encode()

	if err != nil {
		return nil, &errors.Error{
			Code: errors.EInvalid,
			Err:  err,
		}
	}
	k := make([]byte, platform.IDLength+len(name))
	copy(k, orgID)
	copy(k[platform.IDLength:], name)
	return k, nil
}

// uniqueBucketName ensures this bucket is unique for this organization
func (s *Store) uniqueBucketName(ctx context.Context, tx kv.Tx, oid platform.ID, uname string) error {
	key, err := bucketIndexKey(oid, uname)
	if err != nil {
		return err
	}
	if len(key) == 0 {
		return ErrNameisEmpty
	}

	idx, err := tx.Bucket(bucketIndex)
	if err != nil {
		return err
	}

	_, err = idx.Get(key)
	// if not found then this is  _unique_.
	if kv.IsNotFound(err) {
		return nil
	}

	// no error means this is not unique
	if err == nil {
		return BucketAlreadyExistsError(uname)
	}

	// any other error is some sort of internal server error
	return errors.ErrInternalServiceError(err)
}

func unmarshalBucket(v []byte) (*influxdb.Bucket, error) {
	u := &influxdb.Bucket{}
	if err := json.Unmarshal(v, u); err != nil {
		return nil, ErrCorruptBucket(err)
	}

	return u, nil
}

func marshalBucket(u *influxdb.Bucket) ([]byte, error) {
	v, err := json.Marshal(u)
	if err != nil {
		return nil, ErrUnprocessableBucket(err)
	}

	return v, nil
}

func (s *Store) GetBucket(ctx context.Context, tx kv.Tx, id platform.ID) (bucket *influxdb.Bucket, retErr error) {
	defer func() {
		retErr = errors.ErrInternalServiceError(retErr, errors.WithErrorOp(influxdb.OpFindBucketByID))
	}()
	encodedID, err := id.Encode()
	if err != nil {
		return nil, InvalidOrgIDError(err)
	}

	b, err := tx.Bucket(bucketBucket)
	if err != nil {
		return nil, err
	}

	v, err := b.Get(encodedID)
	if kv.IsNotFound(err) {
		return nil, ErrBucketNotFound
	}

	if err != nil {
		return nil, err
	}

	return unmarshalBucket(v)
}

func (s *Store) GetBucketByName(ctx context.Context, tx kv.Tx, orgID platform.ID, n string) (bucket *influxdb.Bucket, retErr error) {
	defer func() {
		retErr = errors.ErrInternalServiceError(retErr, errors.WithErrorOp(influxdb.OpFindBucket))
	}()
	key, err := bucketIndexKey(orgID, n)
	if err != nil {
		return nil, errors.ErrInternalServiceError(err, errors.WithErrorCode(errors.EInvalid))
	}

	idx, err := tx.Bucket(bucketIndex)
	if err != nil {
		return nil, err
	}

	buf, err := idx.Get(key)

	// allow for hard coded bucket names that don't exist in the system
	if kv.IsNotFound(err) {
		return nil, ErrBucketNotFoundByName(n)
	}

	if err != nil {
		return nil, err
	}

	var id platform.ID
	if err := id.Decode(buf); err != nil {
		return nil, err
	}
	return s.GetBucket(ctx, tx, id)
}

type BucketFilter struct {
	Name           *string
	OrganizationID *platform.ID
}

func (s *Store) ListBuckets(ctx context.Context, tx kv.Tx, filter BucketFilter, opt ...influxdb.FindOptions) (buckets []*influxdb.Bucket, retErr error) {
	defer func() {
		retErr = errors.ErrInternalServiceError(retErr, errors.WithErrorOp(influxdb.OpFindBuckets))
	}()
	// this isn't a list action its a `GetBucketByName`
	if (filter.OrganizationID != nil && filter.OrganizationID.Valid()) && filter.Name != nil {
		return nil, invalidBucketListRequest
	}

	if len(opt) == 0 {
		opt = append(opt, influxdb.FindOptions{})
	}
	o := opt[0]

	// if an organization is passed we need to use the index
	if filter.OrganizationID != nil {
		return s.listBucketsByOrg(ctx, tx, *filter.OrganizationID, o)
	}

	b, err := tx.Bucket(bucketBucket)
	if err != nil {
		return nil, err
	}

	var opts []kv.CursorOption
	if o.Descending {
		opts = append(opts, kv.WithCursorDirection(kv.CursorDescending))
	}

	var seek []byte
	if o.After != nil {
		after := (*o.After) + 1
		seek, err = after.Encode()
		if err != nil {
			return nil, err
		}
	}

	cursor, err := b.ForwardCursor(seek, opts...)
	if err != nil {
		return nil, err
	}
	defer errors2.Capture(&retErr, cursor.Close)()

	count := 0
	bs := []*influxdb.Bucket{}
	for k, v := cursor.Next(); k != nil; k, v = cursor.Next() {
		if o.Offset != 0 && count < o.Offset {
			count++
			continue
		}
		b, err := unmarshalBucket(v)
		if err != nil {
			return nil, err
		}

		// check to see if it matches the filter
		if filter.Name == nil || (*filter.Name == b.Name) {
			bs = append(bs, b)
		}

		if o.Limit != 0 && len(bs) >= o.Limit {
			break
		}
	}

	return bs, cursor.Err()
}

func (s *Store) listBucketsByOrg(ctx context.Context, tx kv.Tx, orgID platform.ID, o influxdb.FindOptions) (buckets []*influxdb.Bucket, retErr error) {
	// get the prefix key (org id with an empty name)
	key, err := bucketIndexKey(orgID, "")
	if err != nil {
		return nil, errors.ErrInternalServiceError(err, errors.WithErrorCode(errors.EInvalid))
	}

	idx, err := tx.Bucket(bucketIndex)
	if err != nil {
		return nil, err
	}

	start := key
	opts := []kv.CursorOption{kv.WithCursorPrefix(key)}
	if o.Descending {
		// To list in descending order, we have to find the last entry prefixed
		// by the org ID. AFAICT the only way to do this given our current indexing
		// scheme is to iterate through all entries in the org once, remembering the
		// last-seen key.
		start, err = func() ([]byte, error) {
			cursor, err := idx.ForwardCursor(start, opts...)
			if err != nil {
				return nil, err
			}
			defer errors2.Capture(&retErr, cursor.Close)()

			lastKey := start
			for k, _ := cursor.Next(); k != nil; k, _ = cursor.Next() {
				lastKey = k
			}
			return lastKey, nil
		}()
		if err != nil {
			return nil, err
		}
		// Once we've found the end, walk backwards from it on the next iteration.
		opts = append(opts, kv.WithCursorDirection(kv.CursorDescending))
	}
	cursor, err := idx.ForwardCursor(start, opts...)
	if err != nil {
		return nil, err
	}
	defer errors2.Capture(&retErr, cursor.Close)()
	count := 0
	bs := []*influxdb.Bucket{}
	searchingForAfter := o.After != nil
	for k, v := cursor.Next(); k != nil; k, v = cursor.Next() {
		if o.Offset != 0 && count < o.Offset {
			count++
			continue
		}

		if err != nil {
			return nil, err
		}

		var id platform.ID
		if err := id.Decode(v); err != nil {
			return nil, err
		}
		if searchingForAfter {
			searchingForAfter = id != *o.After
			continue
		}
		b, err := s.GetBucket(ctx, tx, id)
		if err != nil {
			return nil, err
		}

		bs = append(bs, b)

		if o.Limit != 0 && len(bs) >= o.Limit {
			break
		}
	}

	return bs, cursor.Err()
}

func (s *Store) CreateBucket(ctx context.Context, tx kv.Tx, bucket *influxdb.Bucket) (err error) {
	defer func() {
		err = errors.ErrInternalServiceError(err, errors.WithErrorOp(influxdb.OpCreateBucket))
	}()
	// generate new bucket ID
	bucket.ID, err = s.generateSafeID(ctx, tx, bucketBucket, s.BucketIDGen)
	if err != nil {
		return err
	}

	encodedID, err := bucket.ID.Encode()
	if err != nil {
		return InvalidOrgIDError(err)
	}

	if err := s.uniqueBucketName(ctx, tx, bucket.OrgID, bucket.Name); err != nil {
		return err
	}

	bucket.SetCreatedAt(s.now())
	bucket.SetUpdatedAt(s.now())
	idx, err := tx.Bucket(bucketIndex)
	if err != nil {
		return err
	}

	b, err := tx.Bucket(bucketBucket)
	if err != nil {
		return err
	}

	v, err := marshalBucket(bucket)
	if err != nil {
		return err
	}

	ikey, err := bucketIndexKey(bucket.OrgID, bucket.Name)
	if err != nil {
		return err
	}

	if err := idx.Put(ikey, encodedID); err != nil {
		return err
	}

	if err := b.Put(encodedID, v); err != nil {
		return err
	}

	return nil
}

func (s *Store) UpdateBucket(ctx context.Context, tx kv.Tx, id platform.ID, upd influxdb.BucketUpdate) (retBucket *influxdb.Bucket, retErr error) {
	defer func() {
		retErr = errors.ErrInternalServiceError(retErr, errors.WithErrorOp(influxdb.OpUpdateBucket))
	}()
	encodedID, err := id.Encode()
	if err != nil {
		return nil, err
	}

	bucket, err := s.GetBucket(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	bucket.SetUpdatedAt(s.now())
	if upd.Name != nil && bucket.Name != *upd.Name {
		// validation
		if bucket.Type == influxdb.BucketTypeSystem {
			return nil, errRenameSystemBucket
		}

		if err := validBucketName(*upd.Name, bucket.Type); err != nil {
			return nil, err
		}

		if err := s.uniqueBucketName(ctx, tx, bucket.OrgID, *upd.Name); err != nil {
			return nil, ErrBucketNameNotUnique
		}

		idx, err := tx.Bucket(bucketIndex)
		if err != nil {
			return nil, err
		}

		oldIkey, err := bucketIndexKey(bucket.OrgID, bucket.Name)
		if err != nil {
			return nil, err
		}

		if err := idx.Delete(oldIkey); err != nil {
			return nil, err
		}

		bucket.Name = *upd.Name
		newIkey, err := bucketIndexKey(bucket.OrgID, bucket.Name)
		if err != nil {
			return nil, err
		}

		if err := idx.Put(newIkey, encodedID); err != nil {
			return nil, err
		}
	}

	if upd.Description != nil {
		bucket.Description = *upd.Description
	}

	if upd.RetentionPeriod != nil {
		bucket.RetentionPeriod = *upd.RetentionPeriod
	}
	if upd.ShardGroupDuration != nil {
		bucket.ShardGroupDuration = *upd.ShardGroupDuration
	}

	v, err := marshalBucket(bucket)
	if err != nil {
		return nil, err
	}

	b, err := tx.Bucket(bucketBucket)
	if err != nil {
		return nil, err
	}
	if err := b.Put(encodedID, v); err != nil {
		return nil, errors.ErrInternalServiceError(err)
	}

	return bucket, nil
}

func (s *Store) DeleteBucket(ctx context.Context, tx kv.Tx, id platform.ID) (retErr error) {
	defer func() {
		retErr = errors.ErrInternalServiceError(retErr, errors.WithErrorOp(influxdb.OpDeleteBucket))
	}()
	bucket, err := s.GetBucket(ctx, tx, id)
	if err != nil {
		return err
	}

	encodedID, err := id.Encode()
	if err != nil {
		return InvalidOrgIDError(err)
	}

	idx, err := tx.Bucket(bucketIndex)
	if err != nil {
		return err
	}

	ikey, err := bucketIndexKey(bucket.OrgID, bucket.Name)
	if err != nil {
		return err
	}
	if err := idx.Delete(ikey); err != nil {
		return err
	}

	b, err := tx.Bucket(bucketBucket)
	if err != nil {
		return err
	}

	if err := b.Delete(encodedID); err != nil {
		return err
	}

	return nil
}
