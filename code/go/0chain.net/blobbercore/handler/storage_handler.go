package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"0chain.net/core/encryption"
	"github.com/gorilla/mux"

	"0chain.net/blobbercore/stats"
	"go.uber.org/zap"

	"0chain.net/blobbercore/allocation"
	"0chain.net/blobbercore/constants"
	"0chain.net/blobbercore/readmarker"
	"0chain.net/blobbercore/reference"
	"0chain.net/blobbercore/writemarker"
	"0chain.net/core/common"

	. "0chain.net/core/logging"
)

const (
	FORM_FILE_PARSE_MAX_MEMORY = 10 * 1024 * 1024

	DOWNLOAD_CONTENT_FULL  = "full"
	DOWNLOAD_CONTENT_THUMB = "thumbnail"
)

type StorageHandler struct{}

func (fsh *StorageHandler) verifyAllocation(ctx context.Context, tx string,
	readonly bool) (alloc *allocation.Allocation, err error) {

	if len(tx) == 0 {
		return nil, common.NewError("verify_allocation",
			"invalid allocation id")
	}

	alloc, err = allocation.VerifyAllocationTransaction(ctx, tx, readonly)
	if err != nil {
		return nil, common.NewErrorf("verify_allocation",
			"verifying allocation transaction error: %v", err)
	}

	if alloc.Expiration < common.Now() {
		return nil, common.NewError("verify_allocation",
			"use of expired allocation")
	}

	return
}

func (fsh *StorageHandler) verifyAuthTicket(ctx context.Context, authTokenString string, allocationObj *allocation.Allocation, refRequested *reference.Ref, clientID string) (bool, error) {
	if len(authTokenString) == 0 {
		return false, common.NewError("invalid_parameters", "Auth ticket required if data read by anyone other than owner.")
	}
	authToken := &readmarker.AuthTicket{}
	err := json.Unmarshal([]byte(authTokenString), &authToken)
	if err != nil {
		return false, common.NewError("invalid_parameters", "Error parsing the auth ticket for download."+err.Error())
	}
	err = authToken.Verify(allocationObj, clientID)
	if err != nil {
		return false, err
	}
	if refRequested.LookupHash != authToken.FilePathHash {
		authTokenRef, err := reference.GetReferenceFromLookupHash(ctx, authToken.AllocationID, authToken.FilePathHash)
		if err != nil {
			return false, err
		}
		if refRequested.ParentPath != authTokenRef.Path && !strings.HasPrefix(refRequested.ParentPath, authTokenRef.Path+"/") {
			return false, common.NewError("invalid_parameters", "Auth ticket is not valid for the resource being requested")
		}
	}

	return true, nil
}

func (fsh *StorageHandler) GetAllocationDetails(ctx context.Context, r *http.Request) (interface{}, error) {
	if r.Method != "GET" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use GET instead")
	}
	allocationTx := r.FormValue("id")
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, false)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}

	return allocationObj, nil
}

func (fsh *StorageHandler) GetAllocationUpdateTicket(ctx context.Context, r *http.Request) (interface{}, error) {
	if r.Method != "GET" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use GET instead")
	}
	allocationTx := r.FormValue("id")
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, false)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}

	// TODO

	return allocationObj, nil
}

func (fsh *StorageHandler) checkIfFileAlreadyExists(ctx context.Context, allocationID string, path string) *reference.Ref {
	fileReference, err := reference.GetReference(ctx, allocationID, path)
	if err != nil {
		return nil
	}
	return fileReference
}

func (fsh *StorageHandler) GetFileMeta(ctx context.Context, r *http.Request) (interface{}, error) {
	if r.Method == "GET" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use POST instead")
	}
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, true)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}
	allocationID := allocationObj.ID

	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	if len(clientID) == 0 {
		return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
	}

	_ = ctx.Value(constants.CLIENT_KEY_CONTEXT_KEY).(string)

	pathHash, err := pathHashFromReq(r, allocationID)
	if err != nil {
		return nil, err
	}

	fileref, err := reference.GetReferenceFromLookupHash(ctx, allocationID, pathHash)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid file path. "+err.Error())
	}

	if fileref.Type != reference.FILE {
		return nil, common.NewError("invalid_parameters", "Path is not a file.")
	}

	result := fileref.GetListingData(ctx)

	commitMetaTxns, err := reference.GetCommitMetaTxns(ctx, fileref.ID)
	if err != nil {
		Logger.Error("Failed to get commitMetaTxns from refID", zap.Error(err), zap.Any("ref_id", fileref.ID))
	}

	result["commit_meta_txns"] = commitMetaTxns

	collaborators, err := reference.GetCollaborators(ctx, fileref.ID)
	if err != nil {
		Logger.Error("Failed to get collaborators from refID", zap.Error(err), zap.Any("ref_id", fileref.ID))
	}

	result["collaborators"] = collaborators

	authTokenString := r.FormValue("auth_token")

	if (allocationObj.OwnerID != clientID &&
		allocationObj.PayerID != clientID &&
		!reference.IsACollaborator(ctx, fileref.ID, clientID)) || len(authTokenString) > 0 {
		authTicketVerified, err := fsh.verifyAuthTicket(ctx, r.FormValue("auth_token"), allocationObj, fileref, clientID)
		if err != nil {
			return nil, err
		}
		if !authTicketVerified {
			return nil, common.NewError("auth_ticket_verification_failed", "Could not verify the auth ticket.")
		}
		delete(result, "path")
	}

	return result, nil
}

func (fsh *StorageHandler) AddCommitMetaTxn(ctx context.Context, r *http.Request) (interface{}, error) {
	if r.Method == "GET" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use POST instead")
	}
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, true)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}
	allocationID := allocationObj.ID

	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	if len(clientID) == 0 {
		return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
	}

	_ = ctx.Value(constants.CLIENT_KEY_CONTEXT_KEY).(string)

	pathHash, err := pathHashFromReq(r, allocationID)
	if err != nil {
		return nil, err
	}

	fileref, err := reference.GetReferenceFromLookupHash(ctx, allocationID, pathHash)
	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid file path. "+err.Error())
	}

	if fileref.Type != reference.FILE {
		return nil, common.NewError("invalid_parameters", "Path is not a file.")
	}

	authTokenString := r.FormValue("auth_token")

	if clientID != allocationObj.OwnerID || len(authTokenString) > 0 {
		authTicketVerified, err := fsh.verifyAuthTicket(ctx, r.FormValue("auth_token"), allocationObj, fileref, clientID)
		if err != nil {
			return nil, err
		}
		if !authTicketVerified {
			return nil, common.NewError("auth_ticket_verification_failed", "Could not verify the auth ticket.")
		}
	}

	txnID := r.FormValue("txn_id")
	if len(txnID) == 0 {
		return nil, common.NewError("invalid_parameter", "TxnID not present in the params")
	}

	err = reference.AddCommitMetaTxn(ctx, fileref.ID, txnID)
	if err != nil {
		return nil, common.NewError("add_commit_meta_txn_failed", "Failed to add commitMetaTxn with err :"+err.Error())
	}

	result := struct {
		Msg string `json:"msg"`
	}{
		Msg: "Added commitMetaTxn successfully",
	}

	return result, nil
}

func (fsh *StorageHandler) AddCollaborator(ctx context.Context, r *http.Request) (interface{}, error) {
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, true)
	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}

	valid, err := verifySignatureFromRequest(r, allocationObj.OwnerPublicKey)
	if !valid || err != nil {
		return nil, common.NewError("invalid_signature", "Invalid signature")
	}

	allocationID := allocationObj.ID
	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	_ = ctx.Value(constants.CLIENT_KEY_CONTEXT_KEY).(string)

	pathHash, err := pathHashFromReq(r, allocationID)
	if err != nil {
		return nil, err
	}

	fileref, err := reference.GetReferenceFromLookupHash(ctx, allocationID, pathHash)
	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid file path. "+err.Error())
	}

	if fileref.Type != reference.FILE {
		return nil, common.NewError("invalid_parameters", "Path is not a file.")
	}

	collabClientID := r.FormValue("collab_id")
	if len(collabClientID) == 0 {
		return nil, common.NewError("invalid_parameter", "collab_id not present in the params")
	}

	var result struct {
		Msg string `json:"msg"`
	}

	switch r.Method {
	case http.MethodPost:
		if len(clientID) == 0 || clientID != allocationObj.OwnerID {
			return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
		}

		if reference.IsACollaborator(ctx, fileref.ID, collabClientID) {
			result.Msg = "Given client ID is already a collaborator"
			return result, nil
		}

		err = reference.AddCollaborator(ctx, fileref.ID, collabClientID)
		if err != nil {
			return nil, common.NewError("add_collaborator_failed", "Failed to add collaborator with err :"+err.Error())
		}
		result.Msg = "Added collaborator successfully"

	case http.MethodGet:
		collaborators, err := reference.GetCollaborators(ctx, fileref.ID)
		if err != nil {
			return nil, common.NewError("get_collaborator_failed", "Failed to get collaborators from refID with err:"+err.Error())
		}

		return collaborators, nil

	case http.MethodDelete:
		if len(clientID) == 0 || clientID != allocationObj.OwnerID {
			return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
		}

		err = reference.RemoveCollaborator(ctx, fileref.ID, collabClientID)
		if err != nil {
			return nil, common.NewError("delete_collaborator_failed", "Failed to delete collaborator from refID with err:"+err.Error())
		}
		result.Msg = "Removed collaborator successfully"

	default:
		return nil, common.NewError("invalid_method", "Invalid method used. Use POST/GET/DELETE instead")
	}

	return result, nil
}

func (fsh *StorageHandler) GetFileStats(ctx context.Context, r *http.Request) (interface{}, error) {
	if r.Method == "GET" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use POST instead")
	}
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, true)
	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}
	allocationID := allocationObj.ID

	valid, err := verifySignatureFromRequest(r, allocationObj.OwnerPublicKey)
	if !valid || err != nil {
		return nil, common.NewError("invalid_signature", "Invalid signature")
	}

	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	if len(clientID) == 0 || allocationObj.OwnerID != clientID {
		return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
	}

	_ = ctx.Value(constants.CLIENT_KEY_CONTEXT_KEY).(string)

	pathHash, err := pathHashFromReq(r, allocationID)
	if err != nil {
		return nil, err
	}

	fileref, err := reference.GetReferenceFromLookupHash(ctx, allocationID, pathHash)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid file path. "+err.Error())
	}

	if fileref.Type != reference.FILE {
		return nil, common.NewError("invalid_parameters", "Path is not a file.")
	}

	result := fileref.GetListingData(ctx)
	stats, _ := stats.GetFileStats(ctx, fileref.ID)
	wm, _ := writemarker.GetWriteMarkerEntity(ctx, fileref.WriteMarker)
	if wm != nil && stats != nil {
		stats.WriteMarkerRedeemTxn = wm.CloseTxnID
	}
	var statsMap map[string]interface{}
	statsBytes, _ := json.Marshal(stats)
	if err = json.Unmarshal(statsBytes, &statsMap); err != nil {
		return nil, err
	}
	for k, v := range statsMap {
		result[k] = v
	}
	return result, nil
}

func (fsh *StorageHandler) ListEntities(ctx context.Context, r *http.Request) (*ListResult, error) {

	if r.Method == "POST" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use GET instead")
	}
	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, true)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}
	allocationID := allocationObj.ID

	if len(clientID) == 0 {
		return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
	}

	pathHash, err := pathHashFromReq(r, allocationID)
	if err != nil {
		return nil, err
	}

	Logger.Info("Path Hash for list dir :" + pathHash)

	fileref, err := reference.GetReferenceFromLookupHash(ctx, allocationID, pathHash)
	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid path. "+err.Error())
	}
	authTokenString := r.FormValue("auth_token")
	if clientID != allocationObj.OwnerID || len(authTokenString) > 0 {
		authTicketVerified, err := fsh.verifyAuthTicket(ctx, r.FormValue("auth_token"), allocationObj, fileref, clientID)
		if err != nil {
			return nil, err
		}
		if !authTicketVerified {
			return nil, common.NewError("auth_ticket_verification_failed", "Could not verify the auth ticket.")
		}
	}

	dirref, err := reference.GetRefWithChildren(ctx, allocationID, fileref.Path)
	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid path. "+err.Error())
	}

	var result ListResult
	result.AllocationRoot = allocationObj.AllocationRoot
	result.Meta = dirref.GetListingData(ctx)
	if clientID != allocationObj.OwnerID {
		delete(result.Meta, "path")
	}
	result.Entities = make([]map[string]interface{}, len(dirref.Children))
	for idx, child := range dirref.Children {
		result.Entities[idx] = child.GetListingData(ctx)
		if clientID != allocationObj.OwnerID {
			delete(result.Entities[idx], "path")
		}
	}

	return &result, nil
}

func (fsh *StorageHandler) GetReferencePath(ctx context.Context, r *http.Request) (*ReferencePathResult, error) {
	resCh := make(chan *ReferencePathResult)
	errCh := make(chan error)
	go fsh.getReferencePath(ctx, r, resCh, errCh)

	for {
		select {
		case <-ctx.Done():
			return nil, common.NewError("timeout", "timeout reached")
		case result := <-resCh:
			return result, nil
		case err := <-errCh:
			return nil, err
		}
	}
}

func (fsh *StorageHandler) getReferencePath(ctx context.Context, r *http.Request, resCh chan<- *ReferencePathResult, errCh chan<- error) {
	if r.Method == "POST" {
		errCh <- common.NewError("invalid_method", "Invalid method used. Use GET instead")
		return
	}

	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, false)
	if err != nil {
		errCh <- common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
		return
	}
	allocationID := allocationObj.ID

	valid, err := verifySignatureFromRequest(r, allocationObj.OwnerPublicKey)
	if !valid || err != nil {
		errCh <- common.NewError("invalid_signature", "Invalid signature")
		return
	}

	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	if len(clientID) == 0 {
		errCh <- common.NewError("invalid_operation", "Please pass clientID in the header")
		return
	}

	paths, err := pathsFromReq(r)
	if err != nil {
		errCh <- err
		return
	}

	rootRef, err := reference.GetReferencePathFromPaths(ctx, allocationID, paths)
	if err != nil {
		errCh <- err
		return
	}

	refPath := &ReferencePath{ref: rootRef}
	refsToProcess := make([]*ReferencePath, 0)
	refsToProcess = append(refsToProcess, refPath)
	for len(refsToProcess) > 0 {
		refToProcess := refsToProcess[0]
		refToProcess.Meta = refToProcess.ref.GetListingData(ctx)
		if len(refToProcess.ref.Children) > 0 {
			refToProcess.List = make([]*ReferencePath, len(refToProcess.ref.Children))
		}
		for idx, child := range refToProcess.ref.Children {
			childRefPath := &ReferencePath{ref: child}
			refToProcess.List[idx] = childRefPath
			refsToProcess = append(refsToProcess, childRefPath)
		}
		refsToProcess = refsToProcess[1:]
	}

	var latestWM *writemarker.WriteMarkerEntity
	if len(allocationObj.AllocationRoot) == 0 {
		latestWM = nil
	} else {
		latestWM, err = writemarker.GetWriteMarkerEntity(ctx, allocationObj.AllocationRoot)
		if err != nil {
			errCh <- common.NewError("latest_write_marker_read_error", "Error reading the latest write marker for allocation."+err.Error())
			return
		}
	}
	var refPathResult ReferencePathResult
	refPathResult.ReferencePath = refPath
	if latestWM != nil {
		refPathResult.LatestWM = &latestWM.WM
	}

	resCh <- &refPathResult
}

func (fsh *StorageHandler) GetObjectPath(ctx context.Context, r *http.Request) (*ObjectPathResult, error) {
	if r.Method == "POST" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use GET instead")
	}
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, false)
	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}
	allocationID := allocationObj.ID

	valid, err := verifySignatureFromRequest(r, allocationObj.OwnerPublicKey)
	if !valid || err != nil {
		return nil, common.NewError("invalid_signature", "Invalid signature")
	}

	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	if len(clientID) == 0 || allocationObj.OwnerID != clientID {
		return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
	}
	path := r.FormValue("path")
	if len(path) == 0 {
		return nil, common.NewError("invalid_parameters", "Invalid path")
	}

	blockNumStr := r.FormValue("block_num")
	if len(blockNumStr) == 0 {
		return nil, common.NewError("invalid_parameters", "Invalid path")
	}

	blockNum, err := strconv.ParseInt(blockNumStr, 10, 64)
	if err != nil || blockNum < 0 {
		return nil, common.NewError("invalid_parameters", "Invalid block number")
	}

	objectPath, err := reference.GetObjectPath(ctx, allocationID, blockNum)
	if err != nil {
		return nil, err
	}

	var latestWM *writemarker.WriteMarkerEntity
	if len(allocationObj.AllocationRoot) == 0 {
		latestWM = nil
	} else {
		latestWM, err = writemarker.GetWriteMarkerEntity(ctx, allocationObj.AllocationRoot)
		if err != nil {
			return nil, common.NewError("latest_write_marker_read_error", "Error reading the latest write marker for allocation."+err.Error())
		}
	}
	var objPathResult ObjectPathResult
	objPathResult.ObjectPath = objectPath
	if latestWM != nil {
		objPathResult.LatestWM = &latestWM.WM
	}
	return &objPathResult, nil
}

func (fsh *StorageHandler) GetObjectTree(ctx context.Context, r *http.Request) (*ReferencePathResult, error) {
	if r.Method == "POST" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use GET instead")
	}
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, false)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}
	allocationID := allocationObj.ID

	valid, err := verifySignatureFromRequest(r, allocationObj.OwnerPublicKey)
	if !valid || err != nil {
		return nil, common.NewError("invalid_signature", "Invalid signature")
	}

	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	if len(clientID) == 0 || allocationObj.OwnerID != clientID {
		return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
	}
	path := r.FormValue("path")
	if len(path) == 0 {
		return nil, common.NewError("invalid_parameters", "Invalid path")
	}

	rootRef, err := reference.GetObjectTree(ctx, allocationID, path)
	if err != nil {
		return nil, err
	}

	refPath := &ReferencePath{ref: rootRef}
	refsToProcess := make([]*ReferencePath, 0)
	refsToProcess = append(refsToProcess, refPath)
	for len(refsToProcess) > 0 {
		refToProcess := refsToProcess[0]
		refToProcess.Meta = refToProcess.ref.GetListingData(ctx)
		if len(refToProcess.ref.Children) > 0 {
			refToProcess.List = make([]*ReferencePath, len(refToProcess.ref.Children))
		}
		for idx, child := range refToProcess.ref.Children {
			childRefPath := &ReferencePath{ref: child}
			refToProcess.List[idx] = childRefPath
			refsToProcess = append(refsToProcess, childRefPath)
		}
		refsToProcess = refsToProcess[1:]
	}

	var latestWM *writemarker.WriteMarkerEntity
	if len(allocationObj.AllocationRoot) == 0 {
		latestWM = nil
	} else {
		latestWM, err = writemarker.GetWriteMarkerEntity(ctx, allocationObj.AllocationRoot)
		if err != nil {
			return nil, common.NewError("latest_write_marker_read_error", "Error reading the latest write marker for allocation."+err.Error())
		}
	}
	var refPathResult ReferencePathResult
	refPathResult.ReferencePath = refPath
	if latestWM != nil {
		refPathResult.LatestWM = &latestWM.WM
	}
	return &refPathResult, nil
}

func (fsh *StorageHandler) CalculateHash(ctx context.Context, r *http.Request) (interface{}, error) {
	if r.Method != "POST" {
		return nil, common.NewError("invalid_method", "Invalid method used. Use POST instead")
	}
	allocationTx := ctx.Value(constants.ALLOCATION_CONTEXT_KEY).(string)
	allocationObj, err := fsh.verifyAllocation(ctx, allocationTx, false)

	if err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid allocation id passed."+err.Error())
	}
	allocationID := allocationObj.ID

	clientID := ctx.Value(constants.CLIENT_CONTEXT_KEY).(string)
	if len(clientID) == 0 || allocationObj.OwnerID != clientID {
		return nil, common.NewError("invalid_operation", "Operation needs to be performed by the owner of the allocation")
	}

	paths, err := pathsFromReq(r)
	if err != nil {
		return nil, err
	}

	rootRef, err := reference.GetReferencePathFromPaths(ctx, allocationID, paths)
	if err != nil {
		return nil, err
	}

	if _, err := rootRef.CalculateHash(ctx, true); err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	result["msg"] = "Hash recalculated for the given paths"
	return result, nil
}

// verifySignatureFromRequest verifyes signature passed as common.ClientSignatureHeader header.
func verifySignatureFromRequest(r *http.Request, pbK string) (bool, error) {
	sign := encryption.MiraclToHerumiSig(r.Header.Get(common.ClientSignatureHeader))
	if len(sign) < 64 {
		return false, nil
	}

	vars := mux.Vars(r)
	data, ok := vars["allocation"]
	if !ok {
		return false, common.NewError("invalid_params", "Missing allocation tx")
	}

	hash := encryption.Hash(data)
	pbK = encryption.MiraclToHerumiPK(pbK)
	return encryption.Verify(pbK, sign, hash)
}

// pathsFromReq retrieves paths value from request which can be represented as single "path" value or "paths" values,
// marshalled to json.
func pathsFromReq(r *http.Request) ([]string, error) {
	var (
		pathsStr = r.FormValue("paths")
		path     = r.FormValue("path")
		paths    = make([]string, 0)
	)

	if len(pathsStr) == 0 {
		if len(path) == 0 {
			return nil, common.NewError("invalid_parameters", "Invalid path")
		}

		return append(paths, path), nil
	}

	if err := json.Unmarshal([]byte(pathsStr), &paths); err != nil {
		return nil, common.NewError("invalid_parameters", "Invalid path array json")
	}

	return paths, nil
}

func pathHashFromReq(r *http.Request, allocationID string) (string, error) {
	var (
		pathHash = r.FormValue("path_hash")
		path     = r.FormValue("path")
	)
	if len(pathHash) == 0 {
		if len(path) == 0 {
			return "", common.NewError("invalid_parameters", "Invalid path")
		}
		pathHash = reference.GetReferenceLookup(allocationID, path)
	}

	return pathHash, nil
}
