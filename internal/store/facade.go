package store

import (
	"rent-scout/internal/models"
	"rent-scout/internal/store/posts"
	"time"
)

// 导出类型别名：调用方仍可用 store.PostListFilter
type PostListFilter = posts.PostListFilter

// --- posts ---

func (s *Store) InsertPost(p models.RentPost) (bool, error) {
	return s.posts.InsertPost(p)
}

func (s *Store) FetchPendingByStatus(status string, limit int) ([]models.RentPost, error) {
	return s.posts.FetchPendingByStatus(status, limit)
}

func (s *Store) FetchPassedWithoutAI(limit int) ([]models.RentPost, error) {
	return s.posts.FetchPassedWithoutAI(limit)
}

func (s *Store) ListPublishedBetween(from, to time.Time, limit int) ([]models.RentPost, error) {
	return s.posts.ListPublishedBetween(from, to, limit)
}

func (s *Store) MarkStatus(ids []int64, status string) error {
	return s.posts.MarkStatus(ids, status)
}

func (s *Store) FetchPendingByStatuses(statuses []string, limit int) ([]models.RentPost, error) {
	return s.posts.FetchPendingByStatuses(statuses, limit)
}

func (s *Store) ListPosts(f PostListFilter, limit, offset int) ([]models.RentPost, error) {
	return s.posts.ListPosts(f, limit, offset)
}

func (s *Store) CountPosts(f PostListFilter) (int, error) {
	return s.posts.CountPosts(f)
}

func (s *Store) GetPost(id int64) (models.RentPost, bool, error) {
	return s.posts.GetPost(id)
}

func (s *Store) MarkPostHandled(postID int64) error {
	return s.posts.MarkPostHandled(postID)
}

func (s *Store) ClearPostHandled(postID int64) error {
	return s.posts.ClearPostHandled(postID)
}

// --- notify ---

func (s *Store) InsertNotification(postID int64, channel string) (bool, error) {
	return s.notify.InsertNotification(postID, channel)
}

func (s *Store) FetchPendingNotifications(channel string, limit int) ([]models.Notification, error) {
	return s.notify.FetchPendingNotifications(channel, limit)
}

func (s *Store) MarkNotificationSent(postID int64, channel string) error {
	return s.notify.MarkNotificationSent(postID, channel)
}

func (s *Store) MarkNotificationFailed(postID int64, channel, errMsg string, attempts int) error {
	return s.notify.MarkNotificationFailed(postID, channel, errMsg, attempts)
}

func (s *Store) MarkNotificationDead(postID int64, channel, errMsg string) error {
	return s.notify.MarkNotificationDead(postID, channel, errMsg)
}

func (s *Store) NotificationAttempts(postID int64, channel string) (int, error) {
	return s.notify.NotificationAttempts(postID, channel)
}

func (s *Store) InsertFeedback(f models.Feedback) error {
	return s.notify.InsertFeedback(f)
}

func (s *Store) ListNotificationsByPost(postID int64) ([]models.Notification, error) {
	return s.notify.ListNotificationsByPost(postID)
}

func (s *Store) ListFeedbacksByPost(postID int64) ([]models.Feedback, error) {
	return s.notify.ListFeedbacksByPost(postID)
}

func (s *Store) FetchDeadNotifications(limit int) ([]models.Notification, error) {
	return s.notify.FetchDeadNotifications(limit)
}

func (s *Store) ResetNotification(postID int64, channel string) (bool, error) {
	return s.notify.ResetNotification(postID, channel)
}

func (s *Store) FetchNotifyBatch(channels []string, limit int) ([]models.RentPost, error) {
	return s.notify.FetchNotifyBatch(channels, limit)
}

func (s *Store) NotificationStatuses(postIDs []int64, channels []string) (map[int64]map[string]string, error) {
	return s.notify.NotificationStatuses(postIDs, channels)
}
