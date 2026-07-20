// Package core 定义 ModelFuzz Engine、Runtime、Policy、Adapter 和 Oracle
// 之间交换的协议无关数据。
//
// core 刻意不包含调度算法，也不依赖具体被测系统。协议负载对 core 不透明，
// 与协议有关的语义状态只通过 Adapter 提供的 map 和模型事件携带。
package core
