export default {
  "documentProgress": {
    "refresh": "Làm mới",
    "searchButton": "Tìm kiếm",
    "totalCount": "{count} tài liệu",
    "details": "Chi tiết",
    "emptyTitle": "Không có tài liệu phù hợp",
    "document": "Tài liệu",
    "wiki": "Wiki",
    "actions": "Thao tác",
    "states": {
      "pending": "Đang chờ",
      "processing": "Đang xử lý",
      "finalizing": "Đang hoàn tất",
      "completed": "Hoàn tất",
      "failed": "Thất bại",
      "skipped": "Đã bỏ qua",
      "cancelled": "Đã hủy",
      "queued": "Đang chờ",
      "retrying": "Đang thử lại",
      "disabled": "Đã tắt"
    },
    "stages": {
      "docreader": "Phân tích tài liệu",
      "chunking": "Phân đoạn",
      "postprocess": "Hậu xử lý"
    }
  },
  "system": {
    "title": "Thông tin hệ thống",
    "sectionDescription": "Xem thông tin phiên bản hệ thống và cấu hình tài khoản người dùng",
    "loadingInfo": "Đang tải thông tin...",
    "retry": "Thử lại",
    "versionLabel": "Phiên bản ứng dụng",
    "versionDescription": "Phiên bản của dịch vụ ứng dụng (weknora-app)",
    "frontendVersionLabel": "Phiên bản giao diện",
    "frontendVersionDescription": "Phiên bản build của giao diện (weknora-ui)",
    "versionMismatch": "Không khớp với phiên bản ứng dụng",
    "buildTimeLabel": "Thời gian build",
    "buildTimeDescription": "Thời điểm hệ thống được build",
    "goVersionLabel": "Phiên bản Go",
    "goVersionDescription": "Phiên bản ngôn ngữ Go được backend sử dụng",
    "startedAtLabel": "Dịch vụ khởi động lúc",
    "startedAtDescription": "Lần gần nhất tiến trình backend hiện tại khởi động",
    "uptimeLabel": "Thời gian hoạt động",
    "uptimeDescription": "Thời gian dịch vụ đã chạy kể từ lần khởi động này",
    "uptimeDays": "{n} ngày",
    "uptimeHours": "{n} giờ",
    "uptimeMinutes": "{n} phút",
    "uptimeSeconds": "{n} giây",
    "dbVersionLabel": "Phiên bản cơ sở dữ liệu",
    "dbVersionDescription": "Phiên bản migration cơ sở dữ liệu hiện tại",
    "dbMigrationFailedTag": "Migration thất bại",
    "dbMigrationFailedTitle": "Migration cơ sở dữ liệu thất bại",
    "dbMigrationFailedDesc": "Migration cơ sở dữ liệu lúc khởi động không hoàn tất thành công. Một số bảng hoặc chỉ mục có thể bị thiếu, có thể làm hỏng việc nạp Wiki, đồ thị tri thức và các tính năng khác. Hãy kiểm tra hướng dẫn xử lý sự cố bên dưới trước; nếu vấn đề vẫn còn, hãy báo cáo qua đường liên kết.",
    "dbMigrationViewDocs": "Xem hướng dẫn xử lý sự cố",
    "dbMigrationReportIssue": "Không khắc phục được? Báo cáo sự cố",
    "keywordIndexEngineLabel": "Công cụ chỉ mục từ khóa",
    "keywordIndexEngineDescription": "Công cụ chỉ mục từ khóa đang được sử dụng",
    "vectorStoreEngineLabel": "Công cụ kho vector",
    "vectorStoreEngineDescription": "Công cụ kho vector đang được sử dụng",
    "graphDatabaseEngineLabel": "Công cụ cơ sở dữ liệu đồ thị",
    "graphDatabaseEngineDescription": "Công cụ cơ sở dữ liệu đồ thị đang được sử dụng",
    "unknown": "Không xác định",
    "messages": {
      "fetchFailed": "Lấy thông tin hệ thống thất bại",
      "networkError": "Lỗi mạng, vui lòng thử lại sau"
    },
    "globalSettings": {
      "title": "Cài đặt hệ thống",
      "description": "Cấu hình thời gian chạy toàn nền tảng. Các thay đổi lưu có hiệu lực ngay lập tức cho mọi không gian làm việc. Chỉ quản trị viên hệ thống mới xem và chỉnh sửa được.",
      "loading": "Đang tải...",
      "empty": "Không có cài đặt hệ thống nào khả dụng để cấu hình",
      "saving": "Đang lưu",
      "saved": "Đã lưu",
      "saveAnnouncement": "Đã lưu {label}",
      "badgeRequiresRestart": "Yêu cầu khởi động lại",
      "badgeSecret": "Bí mật",
      "badgeHighRisk": "Rủi ro cao",
      "badgeOverride": "Đã ghi đè",
      "badgeOverrideTooltip": "Giá trị này đã được quản trị viên lưu vào cơ sở dữ liệu, ghi đè biến môi trường và giá trị mặc định có sẵn.",
      "modifiedAt": "Sửa đổi lần cuối: {value}",
      "tagInputPlaceholder": "Nhấn Enter để thêm một mục, ví dụ: example.com / *.foo.com / 10.0.0.0/8",
      "priorityHint": {
        "disclosure": "Nguồn cấu hình và mức ưu tiên",
        "tier1": "Các mục đã lưu trên trang này (đánh dấu \"Đã ghi đè\") luôn được ưu tiên — biến môi trường bị bỏ qua đối với các mục đó.",
        "tier2": "Các mục chưa lưu ở đây sẽ dùng biến môi trường, hoặc dùng giá trị mặc định có sẵn nếu không thiết lập biến môi trường.",
        "tier3": "Để đưa một mục trở lại dưới sự kiểm soát của biến môi trường, hãy nhấn nút \"Đặt lại\" trên hàng của mục đó."
      },
      "sections": {
        "access": {
          "title": "Đăng ký & truy cập",
          "description": "Quản lý đăng ký, yêu cầu mật khẩu và quy tắc tạo không gian làm việc."
        },
        "tenant": {
          "title": "Mặc định không gian làm việc",
          "description": "Thiết lập hạn mức ban đầu và hành vi tương thích cho không gian làm việc mới mà không ghi đè các không gian làm việc hiện có."
        },
        "runtime": {
          "title": "Thời gian chạy & đồng thời",
          "description": "Cấu hình dung lượng xử lý đồng thời cho các nhóm worker nền và dịch vụ mô hình.",
          "restartHint": "Cài đặt worker yêu cầu khởi động lại"
        },
        "security": {
          "title": "Bảo mật mạng",
          "description": "Quản lý danh sách cho phép SSRF và việc có cho phép sandbox Docker hay không (docker.sock cục bộ tương đương quyền root trên máy chủ)."
        },
        "other": {
          "title": "Cài đặt khác",
          "description": "Các cài đặt trong bản triển khai này không thuộc một nhóm sản phẩm chuẩn."
        }
      },
      "runtime": {
        "title": "Thời gian chạy hàng đợi tác vụ",
        "description": "Tải hàng đợi nền trực tiếp và dung lượng theo từng tiến trình cho mỗi nhóm worker biệt lập. Bao gồm chi tiết tác vụ và các thao tác an toàn; tự động làm mới mỗi 5 giây.",
        "refresh": "Làm mới",
        "autoRefresh": "Tự động làm mới (mỗi 5 giây)",
        "loading": "Đang tải...",
        "retry": "Thử lại",
        "unavailableTitle": "Hàng đợi tác vụ không khả dụng",
        "unavailable": "Bản triển khai này không có hàng đợi Redis / asynq (chế độ Lite) — không có gì để hiển thị.",
        "empty": "Không có dữ liệu hàng đợi",
        "detailsTitle": "Chi tiết hàng đợi",
        "detailsDescription": "Tải trực tiếp và thời gian chờ cho mỗi luồng xử lý. \"Thất bại (đã dừng)\" nghĩa là tác vụ đã vượt quá số lần thử lại và sẽ không tự động chạy lại nữa.",
        "poolsTitle": "Nhóm worker",
        "poolsDescription": "Mỗi giai đoạn có dung lượng được đảm bảo; core và enrichment cũng có thể mượn từ nhóm đàn hồi dùng chung.",
        "perInstance": "Giá trị thẻ là đang hoạt động/dung lượng của toàn cụm",
        "poolConfigured": "Mỗi tiến trình {value}",
        "poolInstances": "{value} phiên bản",
        "poolUtilization": "Đã sử dụng {value}%",
        "queueCount": "{value} hàng đợi",
        "weightShort": "Trọng số {value}",
        "footnote": "Giá trị thẻ hiển thị đang hoạt động/dung lượng trực tiếp của toàn cụm; cấu hình theo từng tiến trình yêu cầu khởi động lại. Nhóm đàn hồi dùng chung chỉ phục vụ hàng đợi core và enrichment.",
        "updatedAt": "Cập nhật lúc {value}",
        "errors": {
          "generic": "Tải thống kê hàng đợi thất bại"
        },
        "summary": {
          "title": "Tổng quan thời gian chạy",
          "active": "Đang hoạt động",
          "pending": "Đang chờ",
          "retry": "Đang thử lại",
          "archived": "Thất bại (đã dừng)"
        },
        "columns": {
          "queue": "Hàng đợi",
          "active": "Đang hoạt động",
          "pending": "Đang chờ",
          "scheduled": "Đã lên lịch",
          "retry": "Thử lại",
          "archived": "Thất bại",
          "completed": "Hoàn tất",
          "latency": "Chờ lâu nhất",
          "status": "Trạng thái"
        },
        "status": {
          "working": "Đang xử lý",
          "waiting": "Đang chờ",
          "idle": "Rảnh",
          "actionRequired": "Cần thao tác",
          "retrying": "Đang thử lại",
          "paused": "Đã tạm dừng"
        },
        "failedNotice": {
          "title": "{count} tác vụ thất bại cần xử lý",
          "description": "Mở số lượng Thất bại màu đỏ trong bảng bên dưới để kiểm tra nguyên nhân, rồi thử lại sau khi khắc phục sự cố gốc."
        },
        "tasks": {
          "title": "Chi tiết tác vụ · {queue}",
          "description": "Kiểm tra mọi trạng thái tác vụ. Tác vụ đã lên lịch và đang thử lại hiển thị lần chạy tiếp theo trước; các trạng thái khác hiển thị tác vụ mới nhất trước.",
          "listTitle": "Tác vụ {state}",
          "openAria": "Xem {count} tác vụ {state} trong {queue}",
          "unavailable": "Chi tiết tác vụ không khả dụng trong bản triển khai này",
          "empty": "Hàng đợi này không có tác vụ {state}",
          "loadError": "Tải chi tiết tác vụ thất bại",
          "loadMore": "Tải thêm",
          "loadedSummary": "Đã tải {count} — cuộn hoặc chạm để tải thêm",
          "loadedAll": "Đã tải tất cả {count}",
          "loadingMore": "Đang tải thêm…",
          "attempts": "Lần thử {current}/{max}",
          "unknownTarget": "Không xác định được đối tượng liên quan",
          "knowledgeBaseLabel": "ID kho tri thức",
          "knowledgeLabel": "ID tài liệu",
          "taskIDLabel": "ID tác vụ nghiệp vụ",
          "tenantLabel": "ID không gian làm việc",
          "sourceLabel": "ID nguồn",
          "targetLabel": "ID đích",
          "sourceKBLabel": "Kho tri thức nguồn",
          "targetKBLabel": "Kho tri thức đích",
          "dataSourceLabel": "ID nguồn dữ liệu",
          "syncLogLabel": "ID nhật ký đồng bộ",
          "knowledgeCountLabel": "Số tài liệu",
          "enqueuedAt": "Đưa vào hàng đợi",
          "startedAt": "Bắt đầu",
          "nextProcessAt": "Lần chạy tiếp theo",
          "lastFailedAt": "Thất bại lần cuối",
          "completedAt": "Hoàn tất",
          "deadline": "Hạn chót",
          "worker": "Worker",
          "health": "Tình trạng thời gian chạy",
          "orphaned": "Mất tín hiệu heartbeat của worker; đang chờ khôi phục",
          "cancel": "Hủy tác vụ",
          "cancelConfirm": "Thao tác này dùng quy trình hủy nghiệp vụ và cũng dừng các tác vụ liên quan của cùng tài liệu. Hủy chứ?",
          "runNow": "Chạy ngay",
          "runNowConfirm": "Tác vụ này sẽ chuyển sang trạng thái đang chờ ngay lập tức mà không đặt lại số lần thử lại. Tiếp tục chứ?",
          "deleteRecord": "Xóa bản ghi",
          "deleteConfirm": "Thao tác này chỉ xóa bản ghi khỏi hàng đợi này. Nó sẽ không chạy hay hoàn tất tác vụ gốc, và nhật ký lịch sử vẫn được giữ lại. Xóa chứ?",
          "purgeArchived": "Xóa tất cả tác vụ thất bại",
          "purgeArchivedConfirm": "Xóa toàn bộ {count} bản ghi thất bại khỏi hàng đợi này cùng lúc. Thao tác này chỉ xóa các tác vụ thất bại trong hàng đợi — không ảnh hưởng đến tác vụ đang chạy hoặc đang chờ, cũng không khôi phục trạng thái nghiệp vụ của các tài liệu đã thất bại. Xóa chứ?",
          "purgeArchivedSuccess": "Đã xóa {count} tác vụ thất bại",
          "purgeArchivedError": "Xóa tác vụ thất bại không thành công",
          "stateFilter": "Lọc theo trạng thái tác vụ",
          "states": {
            "active": "Đang hoạt động",
            "pending": "Đang chờ",
            "scheduled": "Đã lên lịch",
            "retry": "Đang thử lại",
            "archived": "Thất bại",
            "completed": "Hoàn tất"
          },
          "guides": {
            "active": "Kiểm tra worker, thời gian bắt đầu, hạn chót và trạng thái mồ côi. Chỉ cung cấp Hủy khi trạng thái nghiệp vụ có thể cập nhật an toàn.",
            "pending": "Tác vụ đang chờ chưa được nhận xử lý. Hủy sẽ cập nhật trạng thái nghiệp vụ thay vì chỉ xóa dữ liệu Redis.",
            "scheduled": "Tác vụ đã lên lịch có thể chạy sớm; tác vụ pipeline tài liệu cũng có thể dùng cơ chế hủy nghiệp vụ an toàn.",
            "retry": "Dùng lỗi gần nhất, số lần thử và thời gian chạy tiếp theo để quyết định chạy ngay hay hủy.",
            "archived": "Khắc phục nguyên nhân gốc trước khi chạy lại. Xóa bản ghi không hoàn tất tác vụ nghiệp vụ gốc.",
            "completed": "Chỉ hiển thị các tác vụ hoàn tất gần đây có lưu kết quả. Không có thao tác quản lý nào khả dụng."
          },
          "actionSuccess": {
            "cancel": "Đã hủy tác vụ",
            "run_now": "Tác vụ đã chuyển sang đang chờ",
            "delete": "Đã xóa bản ghi thất bại"
          },
          "actionError": {
            "cancel": "Hủy tác vụ thất bại",
            "run_now": "Chạy tác vụ thất bại",
            "delete": "Xóa bản ghi thất bại"
          },
          "taskTypes": {
            "documentProcess": "Phân tích tài liệu",
            "manualProcess": "Xử lý lại thủ công",
            "temporaryDocumentProcess": "Phân tích tệp đính kèm trò chuyện",
            "postProcess": "Hậu xử lý tài liệu",
            "summary": "Tạo tóm tắt",
            "tableSummary": "Tạo tóm tắt bảng",
            "question": "Tạo câu hỏi",
            "multimodal": "Xử lý đa phương thức hình ảnh",
            "graph": "Trích xuất đồ thị tri thức",
            "sync": "Đồng bộ nguồn dữ liệu",
            "faqImport": "Nhập FAQ",
            "batchReparse": "Phân tích lại hàng loạt",
            "batchDelete": "Xóa hàng loạt",
            "move": "Di chuyển tài liệu",
            "indexDelete": "Xóa chỉ mục",
            "kbClone": "Nhân bản kho tri thức",
            "kbDelete": "Xóa kho tri thức",
            "wikiIngest": "Tạo nội dung Wiki",
            "wikiFinalize": "Hoàn tất Wiki"
          }
        },
        "models": {
          "title": "Đồng thời mô hình",
          "description": "Mức đồng thời thực tế của dịch vụ mô hình từ các tác vụ nền. Phần trên lên lịch tác vụ; phần này hiển thị giai đoạn điều tiết dịch vụ mô hình riêng biệt.",
          "scope": "Đang hoạt động tính theo toàn cụm · đang chờ tính theo cục bộ",
          "disabled": "Quản trị đồng thời mô hình nền đang bị tắt. Hãy cấu hình giới hạn đồng thời mô hình mặc định trong Cài đặt chung.",
          "empty": "Chưa có hoạt động mô hình nào. Một mô hình sẽ xuất hiện sau lệnh gọi nền đầu tiên của nó.",
          "backgroundOnly": "Chỉ tính tác vụ nền; trò chuyện tương tác không được tính",
          "columns": {
            "model": "ID mô hình",
            "active": "Đang xử lý",
            "waiting": "Đang bị điều tiết",
            "usage": "Đồng thời"
          },
          "status": {
            "queued": "Đang điều tiết",
            "full": "Đã đạt giới hạn"
          }
        },
        "pools": {
          "core": "Phân tích cốt lõi",
          "postprocess": "Điều phối hậu xử lý",
          "enrichment": "Làm giàu dữ liệu",
          "maintenance": "Bảo trì & đồng bộ",
          "shared": "Đàn hồi dùng chung",
          "wiki": "Nhóm Wiki"
        },
        "poolDescriptions": {
          "core": "Dung lượng phân tích tài liệu và thủ công được đảm bảo",
          "postprocess": "Hoàn tất phân tích và phân luồng làm giàu dữ liệu",
          "enrichment": "Tóm tắt, hình ảnh, đồ thị và tạo câu hỏi",
          "maintenance": "Đồng bộ nguồn, xử lý hàng loạt và dọn dẹp khi xóa",
          "shared": "Được core hoặc enrichment mượn tùy theo lượng việc tồn đọng",
          "wiki": "Tạo nội dung Wiki và hoàn tất toàn cục"
        },
        "queueNames": {
          "default": "Phân tích tài liệu",
          "chat_attachment": "Tệp đính kèm trò chuyện",
          "postprocess": "Hậu xử lý",
          "summary": "Tóm tắt",
          "sync": "Đồng bộ nguồn",
          "low": "Bảo trì & hàng loạt",
          "multimodal": "Đa phương thức",
          "graph": "Trích xuất đồ thị",
          "question": "Câu hỏi",
          "wiki": "Pipeline Wiki"
        },
        "queueDescriptions": {
          "default": "Phân tích tài liệu, phân tích lại thủ công",
          "chat_attachment": "Phân tích tệp tải lên trong phạm vi phiên trò chuyện",
          "postprocess": "Hoàn tất phân tích, phân luồng làm giàu dữ liệu",
          "summary": "Tóm tắt tài liệu & bảng",
          "sync": "Đồng bộ thủ công & theo lịch",
          "low": "Nhập FAQ, phân tích lại hàng loạt, dọn dẹp",
          "multimodal": "OCR hình ảnh, chú thích thị giác",
          "graph": "Trích xuất đồ thị cấp đoạn (chunk)",
          "question": "Tạo câu hỏi cấp đoạn (chunk)",
          "wiki": "Tạo nội dung, hoàn tất chỉ mục"
        }
      },
      "keyLabels": {
        "auth": {
          "registration_mode": "Chế độ đăng ký",
          "default_tenant_mode": "Cấp phát không gian làm việc mặc định",
          "complex_password_enabled": "Yêu cầu mật khẩu phức tạp"
        },
        "ssrf": {
          "whitelist": "Danh sách cho phép bảo vệ SSRF"
        },
        "sandbox": {
          "docker_enabled": "Bật sandbox Docker"
        },
        "tenant": {
          "max_owned_per_user": "Số không gian làm việc tối đa mỗi người dùng được sở hữu",
          "self_service_creation_enabled": "Cho phép tự tạo không gian làm việc",
          "default_storage_quota_gb": "Hạn mức lưu trữ mặc định cho không gian làm việc mới (GB)",
          "default_storage_quota_mb": "Hạn mức lưu trữ mặc định cho không gian làm việc mới (MB)",
          "auto_create_api_key": "Tự động tạo API key cho không gian làm việc mới",
          "auto_accept_invitation": "Tự động thêm người dùng đã đăng ký được mời"
        },
        "asynq": {
          "core_concurrency": "Đồng thời phân tích cốt lõi được đảm bảo",
          "postprocess_concurrency": "Đồng thời hậu xử lý",
          "enrichment_concurrency": "Đồng thời làm giàu dữ liệu được đảm bảo",
          "maintenance_concurrency": "Đồng thời bảo trì",
          "shared_concurrency": "Đồng thời đàn hồi dùng chung",
          "wiki_concurrency": "Đồng thời worker Wiki"
        },
        "model": {
          "max_concurrency": "Giới hạn đồng thời mặc định theo từng mô hình"
        }
      },
      "keyDescriptions": {
        "auth": {
          "registration_mode": "Chế độ đăng ký. Đăng ký mở cho phép bất kỳ ai tạo tài khoản; đăng ký theo lời mời yêu cầu đường liên kết mời hợp lệ; đăng ký bị tắt ngăn việc tạo tài khoản mới trong khi các tài khoản hiện có vẫn có thể chấp nhận lời mời. Thay đổi có hiệu lực ngay lập tức.",
          "default_tenant_mode": "Cấp phát không gian làm việc sau khi đăng ký công khai. create_personal tạo một không gian làm việc với vai trò Chủ sở hữu; tenantless chỉ tạo tài khoản cho đến khi người dùng chấp nhận lời mời hoặc tạo không gian làm việc. Chỉ áp dụng cho người dùng mới.",
          "complex_password_enabled": "Có yêu cầu mật khẩu phức tạp hay không. Khi bật, mật khẩu phải chứa chữ hoa, chữ thường, số và ký tự đặc biệt. Thay đổi có hiệu lực ngay lập tức và chỉ áp dụng cho người dùng mới đăng ký hoặc khi đổi/đặt lại mật khẩu mới. Ký tự đặc biệt bao gồm {specialChars}"
        },
        "ssrf": {
          "whitelist": "Danh sách cho phép bảo vệ SSRF. Chấp nhận các mục như example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1. Có hiệu lực ngay sau khi lưu. Biến môi trường SSRF_WHITELIST_EXTRA vẫn do người triển khai quản lý và không bị ghi đè ở đây."
        },
        "sandbox": {
          "docker_enabled": "Cho phép backend sandbox Docker. Một docker.sock cục bộ tương đương quyền root trên máy chủ, nên mặc định mục này tắt. Chỉ quản trị viên hệ thống mới bật được; thay đổi có hiệu lực ngay lập tức. Chỉ bật trên bản cài đặt một nút riêng tư có gắn socket daemon hoặc dùng điểm cuối tcp:// từ xa được bảo vệ bằng TLS."
        },
        "tenant": {
          "max_owned_per_user": "Số không gian làm việc tối đa mà một người dùng không phải superuser có thể sở hữu qua việc tự tạo. Được đọc mỗi khi tạo không gian làm việc và có hiệu lực ngay sau khi lưu. 0 dùng giá trị mặc định có sẵn là 10; giá trị âm sẽ tắt hoàn toàn giới hạn này (không khuyến nghị trên các bản triển khai công khai).",
          "self_service_creation_enabled": "Người dùng không phải superuser có được tự tạo không gian làm việc hay không. Khi tắt, người dùng thông thường chỉ có thể tham gia không gian làm việc hiện có qua lời mời; superuser liên không gian làm việc vẫn được miễn trừ. Có hiệu lực ngay lập tức.",
          "default_storage_quota_gb": "Hạn mức lưu trữ mặc định (GB) được cấp khi tạo không gian làm việc mới, bao gồm vector, bản gốc, văn bản, chỉ mục và dữ liệu liên quan. Chỉ được đọc tại thời điểm tạo — thay đổi chỉ áp dụng cho không gian làm việc mới tạo và không cập nhật ngược cho các không gian làm việc hiện có. 0 hoặc giá trị âm dùng giá trị mặc định có sẵn là 10 GB.",
          "default_storage_quota_mb": "Bao gồm cả không gian làm việc cá nhân được tạo khi đăng ký. Giá trị dương sẽ ghi đè thiết lập GB; giá trị bằng 0 hoặc âm sẽ dùng GB. Mỗi thiết lập được phân giải từ cơ sở dữ liệu, rồi đến biến môi trường, rồi đến giá trị mặc định có sẵn. Thay đổi chỉ ảnh hưởng đến không gian làm việc mới tạo.",
          "auto_create_api_key": "Tự động tạo một API key full_access cho không gian làm việc mới và trả về token dạng văn bản thuần trong phản hồi tạo. Chỉ dùng cho các tích hợp phụ thuộc vào hành vi cũ; mặc định bị tắt và khuyến nghị tạo API key một cách tường minh.",
          "auto_accept_invitation": "Khi bật, mời một người dùng đã đăng ký qua email sẽ thêm họ làm thành viên ngay lập tức thay vì chờ xác nhận từ hộp thư. Khi tắt, người được mời phải chấp nhận từ hộp thư của họ. Có hiệu lực ngay lập tức."
        },
        "asynq": {
          "core_concurrency": "Đồng thời theo từng tiến trình được đảm bảo cho phân tích tài liệu và thủ công. Core cũng có thể mượn từ nhóm đàn hồi dùng chung. Tối thiểu là 1; yêu cầu khởi động lại dịch vụ.",
          "postprocess_concurrency": "Đồng thời riêng theo từng tiến trình cho việc hoàn tất phân tích nhẹ và phân luồng làm giàu dữ liệu. Tối thiểu là 1; yêu cầu khởi động lại dịch vụ.",
          "enrichment_concurrency": "Đồng thời theo từng tiến trình được đảm bảo cho tóm tắt, hình ảnh, đồ thị và tạo câu hỏi. Enrichment cũng có thể mượn từ nhóm đàn hồi dùng chung. Tối thiểu là 1; yêu cầu khởi động lại dịch vụ.",
          "maintenance_concurrency": "Đồng thời theo từng tiến trình cho đồng bộ nguồn, xử lý hàng loạt và dọn dẹp, được cách ly hoàn toàn khỏi pipeline hướng người dùng. Tối thiểu là 1; yêu cầu khởi động lại dịch vụ.",
          "shared_concurrency": "Đồng thời đàn hồi theo từng tiến trình được chia sẻ giữa phân tích cốt lõi và làm giàu dữ liệu; bên nào có việc tồn đọng sẽ tự động mượn. Tối thiểu là 1; yêu cầu khởi động lại dịch vụ.",
          "wiki_concurrency": "Đồng thời theo từng tiến trình cho nhóm worker Wiki riêng, được cách ly khỏi các tác vụ thượng nguồn. Tối thiểu là 1; yêu cầu khởi động lại dịch vụ."
        },
        "model": {
          "max_concurrency": "Giới hạn mặc định cho số lệnh gọi nền đồng thời (nạp dữ liệu/làm giàu) tới một mô hình, theo ID mô hình và dùng chung giữa các bản sao (replica). Được đọc ở mỗi lệnh gọi và áp dụng ngay không cần khởi động lại. 0 hoặc giá trị âm sẽ tắt giới hạn mặc định (mỗi mô hình vẫn tuân theo giới hạn riêng được cấu hình trong quản lý mô hình). Chỉ ảnh hưởng đến tác vụ nền, không ảnh hưởng đến trò chuyện tương tác."
        }
      },
      "enumLabels": {
        "auth": {
          "registration_mode": {
            "self_serve": "Tự đăng ký (bất kỳ ai cũng có thể đăng ký)",
            "invite_register": "Đăng ký theo lời mời (cần đường liên kết hợp lệ)",
            "invite_only": "Đăng ký bị tắt (tài khoản hiện có vẫn chấp nhận lời mời được)"
          },
          "default_tenant_mode": {
            "create_personal": "Tạo không gian làm việc cá nhân",
            "tenantless": "Không tạo không gian làm việc"
          }
        }
      },
      "confirm": {
        "confirmBtn": "Xác nhận lưu",
        "cancelBtn": "Hủy",
        "emptyValue": "(trống)",
        "bodyAuthRegistrationMode": "Sắp thay đổi \"{label}\" thành: {value}\n\nNếu chuyển sang self_serve, bất kỳ ai trên internet công cộng cũng có thể đăng ký tài khoản — vui lòng xác nhận đây đúng là điều bạn muốn.",
        "bodySandboxDockerEnabled": "Khi bật, quản trị viên không gian làm việc có thể trỏ sandbox vào daemon Docker cục bộ. Một docker.sock cục bộ tương đương quyền root trên máy chủ. Chỉ dùng điều này trên bản cài đặt một nút riêng tư có gắn daemon hoặc dùng điểm cuối tcp:// từ xa được bảo vệ bằng TLS."
      },
      "listConfirm": {
        "ssrf": {
          "whitelist": {
            "add": {
              "header": "Thêm mục vào danh sách cho phép SSRF",
              "body": "Thêm {entry} vào danh sách cho phép SSRF? Các host / IP / CIDR khớp với mục này sẽ bỏ qua bảo vệ SSRF và có thể cho phép agent truy cập các dịch vụ nội bộ. Chỉ thêm những mục bạn hoàn toàn tin tưởng.",
              "confirmBtn": "Thêm"
            },
            "remove": {
              "header": "Xóa mục khỏi danh sách cho phép SSRF",
              "body": "Xóa {entry} khỏi danh sách cho phép SSRF? Sau khi xóa, mục này sẽ lại bị bảo vệ SSRF chặn.",
              "confirmBtn": "Xóa"
            }
          }
        }
      },
      "messages": {
        "loadFailed": "Tải cài đặt hệ thống thất bại",
        "saveSuccess": "Đã lưu",
        "saveFailed": "Lưu thất bại"
      },
      "reset": {
        "label": "Đặt lại",
        "tooltip": "Xóa giá trị ghi đè trên giao diện và quay về biến môi trường hoặc giá trị mặc định có sẵn",
        "confirmBtn": "Xác nhận đặt lại",
        "confirmBody": "Đặt lại \"{label}\"? Thao tác này sẽ xóa giá trị ghi đè trong cơ sở dữ liệu và quay về biến môi trường hoặc giá trị mặc định có sẵn.",
        "success": "Đã đặt lại về mặc định",
        "failed": "Đặt lại thất bại"
      },
      "userList": {
        "title": "Người dùng nền tảng",
        "rowLabel": "Quản lý người dùng",
        "rowDesc": "Xem mọi người dùng đã đăng ký trên các không gian làm việc, có phân trang và lọc theo tên đăng nhập/email.",
        "searchPlaceholder": "Lọc theo tên đăng nhập hoặc email (trang hiện tại)",
        "refresh": "Làm mới",
        "active": "Đang hoạt động",
        "inactive": "Đã vô hiệu hóa",
        "colUsername": "Tên đăng nhập",
        "colStatus": "Trạng thái",
        "colRole": "Vai trò",
        "colCreated": "Đã tạo",
        "colAction": "Thao tác",
        "prev": "Trước",
        "next": "Sau",
        "pageCount": "Hiển thị {shown} / {total} trên trang",
        "enable": "Kích hoạt",
        "disable": "Vô hiệu hóa",
        "resetPassword": "Đặt lại mật khẩu",
        "confirmDisable": "Vô hiệu hóa tài khoản {name}? Các phiên đang hoạt động của họ sẽ bị thu hồi ngay lập tức.",
        "confirmEnable": "Kích hoạt tài khoản {name}?",
        "enabledMsg": "Đã kích hoạt {name}",
        "disabledMsg": "Đã vô hiệu hóa {name}",
        "actionFailed": "Thao tác thất bại",
        "loadFailed": "Tải danh sách người dùng thất bại",
        "roleAdmin": "Quản trị viên hệ thống",
        "roleUser": "Người dùng thông thường",
        "confirmRevoke": "Thu hồi quyền quản trị hệ thống của {name}?",
        "confirmPromote": "Thăng {name} lên quản trị viên hệ thống?",
        "revokeBtn": "Thu hồi",
        "promoteBtn": "Thăng cấp",
        "revokedMsg": "Đã thu hồi quyền quản trị của {name}",
        "promotedMsg": "Đã thăng {name} lên quản trị viên hệ thống",
        "roleFailed": "Đổi vai trò thất bại"
      },
      "passwordReset": {
        "dialogTitle": "Đặt lại mật khẩu của người dùng khác",
        "warning": "Đây là thao tác rủi ro cao. Hãy kiểm tra kỹ email người dùng. Bạn không thể đặt lại mật khẩu của chính mình ở đây.",
        "emailLabel": "Email người dùng",
        "newPasswordLabel": "Mật khẩu mới",
        "newPasswordPlaceholder": "Nhập mật khẩu mới",
        "confirmPasswordLabel": "Xác nhận mật khẩu mới",
        "confirmPasswordPlaceholder": "Nhập lại mật khẩu mới",
        "confirmBtn": "Xác nhận đặt lại",
        "success": "Đã đặt lại mật khẩu; các phiên hiện có của người dùng đã bị thu hồi",
        "failed": "Đặt lại mật khẩu thất bại"
      },
      "createUser": {
        "action": "Tạo người dùng",
        "dialogTitle": "Tạo người dùng mới",
        "warning": "Đây là thao tác rủi ro cao. Hãy kiểm tra kỹ tên đăng nhập và email — đây là cách người dùng mới sẽ đăng nhập.",
        "usernameLabel": "Tên đăng nhập",
        "usernamePlaceholder": "2-50 ký tự",
        "emailLabel": "Email",
        "emailPlaceholder": "Nhập một địa chỉ email hợp lệ",
        "autoGenerateLabel": "Tự động tạo mật khẩu ngẫu nhiên",
        "newPasswordLabel": "Mật khẩu mới",
        "newPasswordPlaceholder": "8-32 ký tự, gồm chữ và số",
        "confirmPasswordLabel": "Xác nhận mật khẩu mới",
        "confirmPasswordPlaceholder": "Nhập lại mật khẩu mới",
        "confirmBtn": "Tạo người dùng",
        "success": "Đã tạo người dùng",
        "successIdempotent": "Người dùng đã tồn tại; không có thay đổi nào được thực hiện",
        "missingPassword": "Người dùng đã được tạo nhưng mật khẩu được sinh ra không được trả về. Hãy đặt lại mật khẩu để khôi phục quyền truy cập.",
        "failed": "Tạo người dùng thất bại",
        "validation": {
          "usernameRequired": "Nhập tên đăng nhập",
          "usernameLength": "Tên đăng nhập phải có 2-50 ký tự",
          "emailRequired": "Nhập địa chỉ email",
          "emailInvalid": "Nhập một địa chỉ email hợp lệ",
          "confirmRequired": "Nhập lại mật khẩu mới",
          "passwordMismatch": "Mật khẩu không khớp"
        },
        "generated": {
          "successTitle": "Đã tạo người dùng",
          "successBody": "Một mật khẩu ngẫu nhiên đã được sinh ra cho người dùng này. Mật khẩu chỉ hiển thị một lần duy nhất.",
          "usernameLabel": "Tên đăng nhập",
          "emailLabel": "Email",
          "passwordLabel": "Mật khẩu được sinh ra",
          "copyBtn": "Sao chép thông tin tài khoản",
          "copySuccess": "Đã sao chép thông tin tài khoản vào clipboard",
          "acknowledgeBtn": "Tôi đã lưu mật khẩu"
        }
      },
      "bulkApply": {
        "label": "Áp dụng cho tất cả không gian làm việc hiện có",
        "tooltip": "Mặc định, lưu giá trị chỉ ảnh hưởng đến không gian làm việc mới; nhấn vào đây để ghi đè cả mọi không gian làm việc hiện có.",
        "confirmBtn": "Xác nhận áp dụng",
        "confirmBody": "Ghi đè hạn mức lưu trữ của mọi không gian làm việc hiện có thành {value} {unit}. Các không gian làm việc có hạn mức đã được bộ phận vận hành tùy chỉnh thủ công cũng sẽ bị ghi đè. Tiếp tục chứ?",
        "success": "Đã cập nhật hạn mức lưu trữ cho {count} không gian làm việc thành {value} {unit}",
        "failed": "Áp dụng cho tất cả không gian làm việc thất bại"
      },
      "audit": {
        "tabLabel": "Nhật ký kiểm toán",
        "description": "Sự kiện ở cấp nền tảng: thay đổi cài đặt hệ thống, cấp/thu hồi quyền quản trị hệ thống, đồng bộ hạn mức hàng loạt. Mới nhất trước.",
        "refresh": "Làm mới",
        "retry": "Thử lại",
        "loading": "Đang tải…",
        "end": "Hết danh sách.",
        "empty": "Chưa có sự kiện kiểm toán cấp nền tảng nào.",
        "forbidden": "Không được phép xem nhật ký kiểm toán nền tảng.",
        "systemActor": "Hệ thống",
        "errors": {
          "generic": "Tải nhật ký kiểm toán thất bại"
        },
        "actorRole": {
          "system_admin": "Quản trị viên hệ thống"
        },
        "columns": {
          "time": "Thời gian",
          "actor": "Người thực hiện",
          "action": "Sự kiện",
          "target": "Đối tượng",
          "path": "Yêu cầu",
          "outcome": "Kết quả"
        },
        "action": {
          "system.setting_changed": "Đã thay đổi cài đặt hệ thống",
          "system.admin_promoted": "Đã cấp quyền quản trị hệ thống",
          "system.api_key_created": "Đã tạo API key nền tảng",
          "system.api_key_revoked": "Đã thu hồi API key nền tảng",
          "system.admin_revoked": "Đã thu hồi quyền quản trị hệ thống",
          "system.user_password_reset": "Đã đặt lại mật khẩu người dùng",
          "system.user_created": "Đã tạo người dùng",
          "system.queue_task_retried": "Tác vụ thất bại đã chạy lại",
          "system.queue_task_deleted": "Đã xóa bản ghi tác vụ thất bại",
          "system.queue_task_run_now": "Tác vụ hàng đợi đã chạy ngay",
          "system.queue_task_cancelled": "Đã hủy tác vụ hàng đợi",
          "system.queue_archived_purged": "Đã xóa tất cả tác vụ thất bại"
        },
        "outcome": {
          "success": "Thành công",
          "denied": "Bị từ chối"
        },
        "target": {
          "bulkQuota": "Đồng bộ hàng loạt: hạn mức lưu trữ mặc định",
          "bulkQuotaDiff": "Đã áp dụng cho {count} không gian làm việc ({gb} GB)",
          "promoteIdempotent": "Đối tượng đã là quản trị viên hệ thống (không thay đổi)",
          "revokeNoop": "Đối tượng vốn không phải quản trị viên hệ thống (không thay đổi)",
          "requiredRole": "Vai trò yêu cầu: {role}",
          "valueNull": "(chưa thiết lập)"
        },
        "expanded": {
          "actorId": "ID người thực hiện",
          "targetUserId": "ID người dùng đối tượng",
          "targetType": "Loại đối tượng",
          "targetId": "ID đối tượng",
          "details": "Chi tiết thô"
        },
        "drawer": {
          "sectionSummary": "Tóm tắt sự kiện",
          "sectionIdentifiers": "ID liên quan",
          "sectionRequest": "Yêu cầu",
          "targetChange": "Thay đổi",
          "requestMethod": "Phương thức"
        }
      }
    }
  }
}
