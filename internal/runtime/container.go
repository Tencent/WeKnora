// Package runtime 애플리케이션 런타임에 대한 의존성 주입 컨테이너를 제공합니다.
// 이 패키지는 uber의 dig 라이브러리를 사용하여 의존성 주입을 관리합니다.
package runtime

import (
	"go.uber.org/dig"
)

// container는 애플리케이션의 전역 의존성 주입 컨테이너입니다.
// 모든 서비스와 컴포넌트는 이를 통해 등록되고 해결됩니다.
var container *dig.Container

// init 의존성 주입 컨테이너를 초기화합니다.
// 프로그램 시작 시 자동으로 호출됩니다.
func init() {
	container = dig.New()
}

// GetContainer 전역 의존성 주입 컨테이너의 참조를 반환합니다.
// 다른 패키지에서 서비스를 등록하거나 가져올 때 사용합니다.
func GetContainer() *dig.Container {
	return container
}
