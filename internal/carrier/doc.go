// Package carrier 提供运营商档案与参数推导（WS-13，WS-18/P1）。
//
// 统一档案模型（preset 内嵌 + JSON 运行时覆盖）；ResolveEffectiveCarrierConfig
// 单函数；LearnedProfileStore 接口（已学习档案持久化，key=MCC/MNC+SPN/GID）；
// WS-18 实现推导引擎（试探→收敛→持久化→下次直用）。
package carrier
