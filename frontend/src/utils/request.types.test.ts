import type { WithStatus } from './request'

// Checked by vue-tsc: primitive branches match the response interceptor at runtime.
type Assert<T extends true> = T
type Same<A, B> = [A] extends [B] ? [B] extends [A] ? true : false : false
export type RequestStatusContracts = [
  Assert<Same<WithStatus<string>, string>>,
  Assert<Same<WithStatus<number>, number>>,
  Assert<Same<WithStatus<null>, null>>,
  Assert<Same<WithStatus<undefined>, undefined>>,
  Assert<Same<WithStatus<Blob>['$httpStatus'], number>>,
  Assert<Same<WithStatus<string[]>['$httpStatus'], number>>,
  Assert<Same<WithStatus<{ success: boolean }>['$httpStatus'], number>>,
]
