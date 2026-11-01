package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

const (
	QuestPersonal = "personal"
	QuestGuild    = "guild"

	QuestPending  = "pending"
	QuestApproved = "approved"
	QuestDenied   = "denied"
)

// QuestParams says what a quest asks for. Count is required; the rest narrow it down.
type QuestParams struct {
	Count       int64  `json:"count"`
	Dungeon     string `json:"dungeon,omitempty"`     // part of the dungeon name, ignoring case
	MinEnchants int    `json:"minEnchants,omitempty"` // drops only
	Bag         string `json:"bag,omitempty"`         // drops only: bag color, ignoring case
}

// QuestTemplate is a quest in the pool.
type QuestTemplate struct {
	ID         int64       `json:"id"`
	Title      string      `json:"title"`
	Type       string      `json:"type"`
	Params     QuestParams `json:"params"`
	Scope      string      `json:"scope"`
	Points     int         `json:"points"`
	Status     string      `json:"status"`
	ProposedBy string      `json:"proposedBy,omitempty"` // IGN; empty = built-in suggestion
	CreatedAt  int64       `json:"createdAt"`
	DecidedBy  string      `json:"decidedBy,omitempty"`
	DecidedAt  *int64      `json:"decidedAt,omitempty"`
}

// QuestAssignment is a quest of one week, for one player (UserID) or a guild.
type QuestAssignment struct {
	ID          int64
	WeekStart   int64
	UserID      *int64
	Guild       string
	TemplateID  *int64
	Title       string
	Type        string
	Params      QuestParams
	Points      int
	CompletedAt *int64
}

func InsertQuestTemplate(ctx context.Context, q Querier, t QuestTemplate, proposedBy *int64, now int64) (int64, error) {
	params, err := json.Marshal(t.Params)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx,
		"INSERT INTO quest_templates (title, type, params, scope, points, status, proposed_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		t.Title, t.Type, string(params), t.Scope, t.Points, QuestPending, proposedBy, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListQuestTemplates returns the pool, newest first; status "" = all.
func ListQuestTemplates(ctx context.Context, q Querier, status, scope string) ([]QuestTemplate, error) {
	query := `SELECT t.id, t.title, t.type, t.params, t.scope, t.points, t.status, COALESCE(p.ign, ''), t.created_at,
			COALESCE(d.ign, ''), t.decided_at
		FROM quest_templates t LEFT JOIN users p ON p.id = t.proposed_by LEFT JOIN users d ON d.id = t.decided_by
		WHERE (? = '' OR t.status = ?) AND (? = '' OR t.scope = ?)
		ORDER BY t.created_at DESC, t.id DESC`
	rows, err := q.QueryContext(ctx, query, status, status, scope, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []QuestTemplate{}
	for rows.Next() {
		var t QuestTemplate
		var params string
		var decided sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Title, &t.Type, &params, &t.Scope, &t.Points, &t.Status, &t.ProposedBy,
			&t.CreatedAt, &t.DecidedBy, &decided); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(params), &t.Params)
		t.DecidedAt = nullInt(decided)
		list = append(list, t)
	}
	return list, rows.Err()
}

// DecideQuestTemplate accepts (status approved, with scope and points) or denies a quest. False if there is none.
func DecideQuestTemplate(ctx context.Context, q Querier, id int64, status, scope string, points int, by, now int64) (bool, error) {
	res, err := q.ExecContext(ctx,
		"UPDATE quest_templates SET status = ?, scope = COALESCE(NULLIF(?, ''), scope), points = CASE WHEN ? > 0 THEN ? ELSE points END, decided_by = ?, decided_at = ? WHERE id = ?",
		status, scope, points, points, by, now, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PendingQuestsBy counts a member's suggestions that wait for a decision.
func PendingQuestsBy(ctx context.Context, q Querier, userID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM quest_templates WHERE proposed_by = ? AND status = ?", userID, QuestPending).Scan(&n)
	return n, err
}

const assignmentColumns = "id, week_start, user_id, COALESCE(guild, ''), template_id, title, type, params, points, completed_at"

func scanAssignments(rows *sql.Rows) ([]QuestAssignment, error) {
	defer rows.Close()
	list := []QuestAssignment{}
	for rows.Next() {
		var a QuestAssignment
		var user, template, completed sql.NullInt64
		var params string
		if err := rows.Scan(&a.ID, &a.WeekStart, &user, &a.Guild, &template, &a.Title, &a.Type, &params, &a.Points, &completed); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(params), &a.Params)
		a.UserID, a.TemplateID, a.CompletedAt = nullInt(user), nullInt(template), nullInt(completed)
		list = append(list, a)
	}
	return list, rows.Err()
}

// PersonalAssignments returns a player's quests of the weeks starting at or after since, oldest first.
func PersonalAssignments(ctx context.Context, q Querier, userID, since int64) ([]QuestAssignment, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+assignmentColumns+" FROM quest_assignments WHERE user_id = ? AND week_start >= ? ORDER BY week_start, id", userID, since)
	if err != nil {
		return nil, err
	}
	return scanAssignments(rows)
}

// GuildAssignments returns a guild's quests of the weeks starting at or after since, oldest first.
func GuildAssignments(ctx context.Context, q Querier, guild string, since int64) ([]QuestAssignment, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+assignmentColumns+" FROM quest_assignments WHERE guild = ? AND week_start >= ? ORDER BY week_start, id", guild, since)
	if err != nil {
		return nil, err
	}
	return scanAssignments(rows)
}

func InsertAssignment(ctx context.Context, q Querier, a QuestAssignment) error {
	params, err := json.Marshal(a.Params)
	if err != nil {
		return err
	}
	var guild any
	if a.Guild != "" {
		guild = a.Guild
	}
	_, err = q.ExecContext(ctx,
		"INSERT INTO quest_assignments (week_start, user_id, guild, template_id, title, type, params, points) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		a.WeekStart, a.UserID, guild, a.TemplateID, a.Title, a.Type, string(params), a.Points)
	return err
}

// CompleteAssignment marks a quest done; false if it already was.
func CompleteAssignment(ctx context.Context, q Querier, id, at int64) (bool, error) {
	res, err := q.ExecContext(ctx, "UPDATE quest_assignments SET completed_at = ? WHERE id = ? AND completed_at IS NULL", at, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AwardPoints gives a player a finished quest's points (once).
func AwardPoints(ctx context.Context, q Querier, assignmentID, userID int64, points int, at int64) error {
	_, err := q.ExecContext(ctx, "INSERT OR IGNORE INTO quest_points (assignment_id, user_id, points, awarded_at) VALUES (?, ?, ?, ?)",
		assignmentID, userID, points, at)
	return err
}

// PointsBetween sums each player's points awarded in [from, to).
func PointsBetween(ctx context.Context, q Querier, from, to int64) (map[int64]int, error) {
	rows, err := q.QueryContext(ctx, "SELECT user_id, SUM(points) FROM quest_points WHERE awarded_at >= ? AND awarded_at < ? GROUP BY user_id", from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	points := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		points[id] = n
	}
	return points, rows.Err()
}
