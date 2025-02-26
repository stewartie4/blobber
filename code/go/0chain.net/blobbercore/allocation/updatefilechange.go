package allocation

import (
	"context"
	"encoding/json"
	"path/filepath"

	"0chain.net/blobbercore/filestore"
	"0chain.net/blobbercore/stats"
	"0chain.net/blobbercore/reference"
	"0chain.net/blobbercore/util"
	"0chain.net/core/common"
	. "0chain.net/core/logging"

	"go.uber.org/zap"
)

type UpdateFileChange struct {
	NewFileChange
}

func (nf *UpdateFileChange) ProcessChange(ctx context.Context, change *AllocationChange, allocationRoot string) (*reference.Ref, error) {

	path, _ := filepath.Split(nf.Path)
	path = filepath.Clean(path)
	tSubDirs := reference.GetSubDirsFromPath(path)

	rootRef, err := reference.GetReferencePath(ctx, nf.AllocationID, nf.Path)
	if err != nil {
		return nil, err
	}

	dirRef := rootRef
	treelevel := 0
	for treelevel < len(tSubDirs) {
		found := false
		for _, child := range dirRef.Children {
			if child.Type == reference.DIRECTORY && treelevel < len(tSubDirs) {
				if child.Name == tSubDirs[treelevel] {
					dirRef = child
					found = true
					break
				}
			}
		}
		if found {
			treelevel++
		} else {
			return nil, common.NewError("invalid_reference_path", "Invalid reference path from the blobber")
		}
	}
	idx := -1
	for i, child := range dirRef.Children {
		if child.Type == reference.FILE && child.Path == nf.Path {
			idx = i
			break
		}
	}
	if idx < 0 {
		Logger.Error("error in file update", zap.Any("change", nf))
		return nil, common.NewError("file_not_found", "File to update not found in blobber")
	}
	existingRef := dirRef.Children[idx]
	existingRef.ActualFileHash = nf.ActualHash
	existingRef.ActualFileSize = nf.ActualSize
	existingRef.MimeType = nf.MimeType
	existingRef.ContentHash = nf.Hash
	existingRef.CustomMeta = nf.CustomMeta
	existingRef.MerkleRoot = nf.MerkleRoot
	existingRef.WriteMarker = allocationRoot
	existingRef.Size = nf.Size
	existingRef.ThumbnailHash = nf.ThumbnailHash
	existingRef.ThumbnailSize = nf.ThumbnailSize
	existingRef.ActualThumbnailHash = nf.ActualThumbnailHash
	existingRef.ActualThumbnailSize = nf.ActualThumbnailSize
	existingRef.EncryptedKey = nf.EncryptedKey

	if err = existingRef.SetAttributes(&nf.Attributes); err != nil {
		return nil, common.NewErrorf("process_update_file_change",
			"setting file attributes: %v", err)
	}

	_, err = rootRef.CalculateHash(ctx, true)
	stats.FileUpdated(ctx, existingRef.ID)
	return rootRef, err
}

func (nf *UpdateFileChange) Marshal() (string, error) {
	ret, err := json.Marshal(nf)
	if err != nil {
		return "", err
	}
	return string(ret), nil
}

func (nf *UpdateFileChange) Unmarshal(input string) error {
	if err := json.Unmarshal([]byte(input), nf); err != nil {
		return err
	}

	return util.UnmarshalValidation(nf)
}

func (nf *UpdateFileChange) DeleteTempFile() error {
	fileInputData := &filestore.FileInputData{}
	fileInputData.Name = nf.Filename
	fileInputData.Path = nf.Path
	fileInputData.Hash = nf.Hash
	err := filestore.GetFileStore().DeleteTempFile(nf.AllocationID, fileInputData, nf.ConnectionID)
	if nf.ThumbnailSize > 0 {
		fileInputData := &filestore.FileInputData{}
		fileInputData.Name = nf.ThumbnailFilename
		fileInputData.Path = nf.Path
		fileInputData.Hash = nf.ThumbnailHash
		err = filestore.GetFileStore().DeleteTempFile(nf.AllocationID, fileInputData, nf.ConnectionID)
	}
	return err
}

func (nfch *UpdateFileChange) CommitToFileStore(ctx context.Context) error {
	fileInputData := &filestore.FileInputData{}
	fileInputData.Name = nfch.Filename
	fileInputData.Path = nfch.Path
	fileInputData.Hash = nfch.Hash
	_, err := filestore.GetFileStore().CommitWrite(nfch.AllocationID, fileInputData, nfch.ConnectionID)
	if err != nil {
		return common.NewError("file_store_error", "Error committing to file store. "+err.Error())
	}
	if nfch.ThumbnailSize > 0 {
		fileInputData := &filestore.FileInputData{}
		fileInputData.Name = nfch.ThumbnailFilename
		fileInputData.Path = nfch.Path
		fileInputData.Hash = nfch.ThumbnailHash
		_, err := filestore.GetFileStore().CommitWrite(nfch.AllocationID, fileInputData, nfch.ConnectionID)
		if err != nil {
			return common.NewError("file_store_error", "Error committing to file store. "+err.Error())
		}
	}
	return nil
}
