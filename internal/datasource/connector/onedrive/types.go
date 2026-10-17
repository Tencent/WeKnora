package onedrive

import "time"

type drive struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DriveType string `json:"driveType"`
	WebURL    string `json:"webUrl"`
	Owner     struct {
		User struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"owner"`
}

type driveItem struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Size                 int64     `json:"size"`
	WebURL               string    `json:"webUrl"`
	LastModifiedDateTime time.Time `json:"lastModifiedDateTime"`
	ParentReference      struct {
		DriveID string `json:"driveId"`
		ID      string `json:"id"`
	} `json:"parentReference"`
	File *struct {
		MimeType string `json:"mimeType"`
	} `json:"file,omitempty"`
	Folder *struct {
		ChildCount int `json:"childCount"`
	} `json:"folder,omitempty"`
	Deleted *struct {
		State string `json:"state"`
	} `json:"deleted,omitempty"`
}

type itemCollection struct {
	Value     []driveItem `json:"value"`
	NextLink  string      `json:"@odata.nextLink"`
	DeltaLink string      `json:"@odata.deltaLink"`
}

type resourceRef struct {
	DriveID string `json:"drive_id"`
	ItemID  string `json:"item_id"`
}

type cursorState struct {
	DeltaLink         string `json:"delta_link"`
	SelectionHash     string `json:"selection_hash"`
	ConnectionVersion uint64 `json:"connection_version"`
}
