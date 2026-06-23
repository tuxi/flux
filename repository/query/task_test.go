package query

import (
	"context"
	"testing"
	"time"

	"flux/domain"
	"flux/domain/entity"
	"flux/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTaskRepositoryListByUserV2ExcludeModeKeys(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&entity.TaskModel{}))

	now := time.Now()
	require.NoError(t, db.Create([]entity.TaskModel{
		taskListModel(1, 100, "video_generation", "image_to_video", now),
		taskListModel(2, 100, "video_generation", "text_to_video", now),
		taskListModel(3, 100, "image_generation", "text_to_image", now),
	}).Error)

	repo := &taskRepository{db: db}
	items, total, err := repo.ListByUserV2(context.Background(), 100, dto.TaskListReq{
		ExcludeModeKeys: " image_to_video, text_to_image ",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	require.Equal(t, int64(2), items[0].ID)
	require.NotNil(t, items[0].ModeKey)
	require.Equal(t, "text_to_video", *items[0].ModeKey)
}

func taskListModel(id int64, userID int64, routeKey string, modeKey string, now time.Time) entity.TaskModel {
	return entity.TaskModel{
		ID:                id,
		UserID:            userID,
		RootID:            id,
		Type:              "workflow",
		Status:            string(domain.TaskSuccess),
		WorkflowVersionID: 1,
		EntryType:         "tool",
		RouteKey:          &routeKey,
		ModeKey:           &modeKey,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}
