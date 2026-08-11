package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/config"
	"github.com/timonwong/jiro/internal/fieldcache"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

type fieldMetadata struct {
	fields           []jira.Field
	principal        fieldcache.Principal
	snapshot         fieldcache.Snapshot
	path             string
	refreshAttempted bool
	refreshErr       error
}

func (a *app) cacheCommand() *cobra.Command {
	command := &cobra.Command{Use: "cache", Short: "Manage local metadata caches"}
	command.AddCommand(a.cacheFieldsRefreshCommand())
	return command
}

func (a *app) cacheFieldsRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the current Principal's field metadata cache",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			client, settings, err := a.client()
			if err != nil {
				return err
			}
			principal, err := jiraPrincipal(command.Context(), client)
			if err != nil {
				return err
			}
			metadata, cacheErr, err := a.fetchFieldMetadata(command.Context(), client, settings, principal)
			if err != nil {
				return err
			}
			if cacheErr != nil {
				return apperr.Wrap(apperr.KindConfig, cacheErr, "write field metadata cache")
			}
			data := map[string]any{
				"refreshed": true, "path": metadata.path, "fieldCount": len(metadata.fields),
				"fetchedAt": metadata.snapshot.FetchedAt, "expiresAt": metadata.snapshot.ExpiresAt,
				"instance": settings.Host, "principal": principal,
			}
			return a.renderMessage(data, fmt.Sprintf("Cached %d fields in %s", len(metadata.fields), metadata.path))
		},
	}
}

func (a *app) loadFieldMetadata(ctx context.Context, client *jira.Client, settings config.Settings) (fieldMetadata, error) {
	principal, err := jiraPrincipal(ctx, client)
	if err != nil {
		return fieldMetadata{}, err
	}
	snapshot, path, readErr := a.fieldStore.Read(settings.Host, principal)
	if readErr == nil && a.fieldStore.Fresh(snapshot) {
		return metadataFromSnapshot(snapshot, path, false), nil
	}

	metadata, cacheErr, refreshErr := a.fetchFieldMetadata(ctx, client, settings, principal)
	if refreshErr == nil {
		if cacheErr != nil {
			a.addFieldCacheWriteWarning(metadata.path, cacheErr)
		}
		return metadata, nil
	}
	if readErr == nil {
		a.addStaleFieldCacheWarning(path, snapshot, refreshErr)
		metadata := metadataFromSnapshot(snapshot, path, true)
		metadata.principal = principal
		metadata.refreshErr = refreshErr
		return metadata, nil
	}
	return fieldMetadata{}, refreshErr
}

func (a *app) fetchFieldMetadata(ctx context.Context, client *jira.Client, settings config.Settings, principal fieldcache.Principal) (fieldMetadata, error, error) {
	fields, err := client.ListFields(ctx)
	if err != nil {
		return fieldMetadata{}, nil, err
	}
	cacheFields := make([]fieldcache.Field, len(fields))
	for i, field := range fields {
		cacheFields[i] = fieldcache.Field{
			ID: field.ID, Name: field.Name, Alias: jira.Slug(field.Name), Custom: field.Custom,
			Type: field.Type, SchemaCustom: field.SchemaCustom,
		}
	}
	path, pathErr := a.fieldStore.Path(settings.Host, principal)
	if pathErr != nil {
		return fieldMetadata{fields: fields, principal: principal, refreshAttempted: true}, pathErr, nil
	}
	snapshot, snapshotErr := a.fieldStore.NewSnapshot(settings.Host, principal, cacheFields)
	if snapshotErr != nil {
		return fieldMetadata{fields: fields, principal: principal, path: path, refreshAttempted: true}, snapshotErr, nil
	}
	writtenPath, writeErr := a.fieldStore.Write(snapshot)
	if writtenPath != "" {
		path = writtenPath
	}
	return fieldMetadata{
		fields: fields, principal: principal, snapshot: snapshot, path: path, refreshAttempted: true,
	}, writeErr, nil
}

func jiraPrincipal(ctx context.Context, client *jira.Client) (fieldcache.Principal, error) {
	user, err := client.Myself(ctx)
	if err != nil {
		return fieldcache.Principal{}, err
	}
	principal := fieldcache.Principal{AccountID: user.AccountID, Username: user.Username}
	if principal.AccountID == "" && principal.Username == "" {
		return fieldcache.Principal{}, apperr.New(apperr.KindAPI, "Jira /myself did not return an accountId or username")
	}
	return principal, nil
}

func customFieldsOnly(fields []jira.Field) []jira.Field {
	custom := make([]jira.Field, 0, len(fields))
	for _, field := range fields {
		if field.Custom || directCustomFieldID.MatchString(field.ID) {
			field.Custom = true
			custom = append(custom, field)
		}
	}
	return custom
}

func metadataFromSnapshot(snapshot fieldcache.Snapshot, path string, refreshAttempted bool) fieldMetadata {
	fields := make([]jira.Field, len(snapshot.Fields))
	for i, field := range snapshot.Fields {
		fields[i] = jira.Field{ID: field.ID, Name: field.Name, Custom: field.Custom, Type: field.Type, SchemaCustom: field.SchemaCustom}
	}
	return fieldMetadata{
		fields: fields, principal: snapshot.Principal, snapshot: snapshot, path: path, refreshAttempted: refreshAttempted,
	}
}

func (a *app) addStaleFieldCacheWarning(path string, snapshot fieldcache.Snapshot, refreshErr error) {
	a.addWarning(output.Warning{
		Code:    "stale_field_cache",
		Message: fmt.Sprintf("using stale field metadata because refresh failed: %v", refreshErr),
		Details: map[string]any{
			"path": path, "fetchedAt": snapshot.FetchedAt.Format(time.RFC3339),
			"expiresAt": snapshot.ExpiresAt.Format(time.RFC3339), "refreshError": refreshErr.Error(),
		},
	})
}

func (a *app) addFieldCacheWriteWarning(path string, cacheErr error) {
	a.addWarning(output.Warning{
		Code:    "field_cache_write_failed",
		Message: fmt.Sprintf("using live field metadata because the cache could not be written: %v", cacheErr),
		Details: map[string]any{"path": path, "cacheError": cacheErr.Error()},
	})
}
