package telegram

import (
	"context"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/m4xvel/monetych_bot/internal/domain"
	"github.com/m4xvel/monetych_bot/internal/features"
	"github.com/m4xvel/monetych_bot/internal/logger"
	"github.com/m4xvel/monetych_bot/internal/usecase"
	"github.com/m4xvel/monetych_bot/pkg/utils"
)

type AcceptPrivacySelectPayload struct {
	ChatID int64 `json:"chat_id"`
}

type SentOrder struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id"`
}

type GameSelectPayload struct {
	ChatID int64 `json:"chat_id"`
	GameID int   `json:"game_id"`
}

type TypeSelectPayload struct {
	ChatID int64 `json:"chat_id"`
	GameID int   `json:"game_id"`
	TypeID int   `json:"type_id"`
}

type OrderSelectPayload struct {
	ChatID int64 `json:"chat_id"`
	GameID int   `json:"game_id"`
	TypeID int   `json:"type_id"`
}

type CancelOrderSelectPayload struct {
	ChatID  int64 `json:"chat_id"`
	OrderID int   `json:"order_id"`
}

type AcceptOrderSelectPayload struct {
	ChatID        int64 `json:"chat_id"`
	OrderID       int   `json:"order_id"`
	UserMessageID int   `json:"user_message_id"`
	ExpertID      int   `json:"expert_id"`
}

type ConfirmedAndDeclinedOrderSelectPayload struct {
	OrderID  int   `json:"order_id"`
	TopicID  int64 `json:"topic_id"`
	ThreadID int64 `json:"thread_id"`
}

type VerificationSelectPayload struct {
	OrderID    int   `json:"order_id"`
	UserChatID int64 `json:"user_chat_id"`
}

type RateSelectPayload struct {
	ChatID  int64 `json:"chat_id"`
	Rate    int   `json:"rate"`
	OrderID int   `json:"order_id"`
}

type SearchPayload struct {
	ChatID  int64 `json:"chat_id"`
	OrderID int   `json:"order_id"`
}

type Handler struct {
	bot                          *tgbotapi.BotAPI
	userService                  *usecase.UserService
	stateService                 *usecase.StateService
	gameService                  *usecase.GameService
	orderService                 *usecase.OrderService
	expertService                *usecase.ExpertService
	supportService               *usecase.SupportService
	orderMessageService          *usecase.OrderMessageService
	orderChatMessageService      *usecase.OrderChatMessageService
	reviewService                *usecase.ReviewService
	callbackTokenService         *usecase.CallbackTokenService
	userPolicyAcceptancesService *usecase.UserPolicyAcceptancesService
	verificationEnabled          bool
	copyMessageQueue             *sendQueue
	router                       *Router
	feature                      *features.Features
	text                         *utils.Messages
	textDynamic                  *utils.Dynamic
}

func NewHandler(
	bot *tgbotapi.BotAPI,
	us *usecase.UserService,
	ss *usecase.StateService,
	gs *usecase.GameService,
	os *usecase.OrderService,
	es *usecase.ExpertService,
	sups *usecase.SupportService,
	rs *usecase.ReviewService,
	oms *usecase.OrderMessageService,
	ocms *usecase.OrderChatMessageService,
	cts *usecase.CallbackTokenService,
	upa *usecase.UserPolicyAcceptancesService,
	verificationEnabled bool,
	privacyPolicyURL string,
	publicOfferURL string,
) *Handler {
	h := &Handler{
		bot:                          bot,
		userService:                  us,
		stateService:                 ss,
		gameService:                  gs,
		orderService:                 os,
		expertService:                es,
		supportService:               sups,
		reviewService:                rs,
		orderMessageService:          oms,
		orderChatMessageService:      ocms,
		callbackTokenService:         cts,
		userPolicyAcceptancesService: upa,
		verificationEnabled:          verificationEnabled,
		copyMessageQueue:             newSendQueue(copyMessageQueueSize),
		router:                       NewRouter(),
		feature:                      features.NewFeatures(),
		text:                         utils.NewMessages(privacyPolicyURL, publicOfferURL),
		textDynamic:                  utils.NewDynamic(privacyPolicyURL, publicOfferURL),
	}

	h.registerRoutes()
	return h
}

func (h *Handler) registerRoutes() {
	h.router.RegisterCommand("start", h.handleStartCommand)
	h.router.RegisterCommand("catalog", h.handlerCatalogCommand)
	h.router.RegisterCommand("support", h.handlerSupportCommand)
	h.router.RegisterCommand("search", h.supportOnly(h.SearchCommand))

	h.router.RegisterCallback("accept_privacy", h.handleAcceptPrivacySelect)
	h.router.RegisterCallback("game:", h.handleGameSelect)
	h.router.RegisterCallback("type:", h.handleTypeSelect)
	h.router.RegisterCallback("order:", h.handleOrderSelect)
	h.router.RegisterCallback("accept:", h.handleAcceptSelect)
	h.router.RegisterCallback("cancel:", h.handlerCancelSelect)
	h.router.RegisterCallback("declined:", h.handleDeclinedSelect)
	h.router.RegisterCallback("declined_reaffirm:",
		h.handleDeclinedReaffirmSelect,
	)
	h.router.RegisterCallback("confirmed:", h.handleConfirmedSelect)
	h.router.RegisterCallback("confirmed_reaffirm:",
		h.handleConfirmedReaffirmSelect,
	)
	if h.verificationEnabled {
		h.router.RegisterCallback("verification:", h.handleVerificationSelect)
		h.router.RegisterCallback("verify:", h.handleVerifySelect)
	}
	h.router.RegisterCallback("back:", h.handleBack)
	h.router.RegisterCallback("accept_client:", h.handleAcceptClientSelect)
	h.router.RegisterCallback("rate:", h.handleRateSelect)

	h.router.RegisterCallback("show_media:", h.handleShowMedia)

	h.router.RegisterMessageHandler(h.handleMessage)
}

func (h *Handler) Route(ctx context.Context, upd tgbotapi.Update) {
	if !h.expertGuard(upd) {
		logger.Log.Warnw("update blocked by expert guard")
		return
	}
	if !h.supportGuard(upd) {
		logger.Log.Warnw("update blocked by support guard")
		return
	}
	if !h.stateGuard(ctx, upd) {
		logger.Log.Warnw("update blocked by state guard")
		return
	}
	if !h.startGuard(ctx, upd) {
		logger.Log.Warnw("update blocked by start guard")
		return
	}
	h.router.Route(ctx, upd)
}

func (h *Handler) deleteOrderMessage(ctx context.Context, orderID int) {
	sentOrders, err := h.orderMessageService.GetByOrder(ctx, orderID)
	if err != nil {
		logger.Log.Errorw("failed to get order messages",
			"order_id", orderID,
			"err", err,
		)
		return
	}

	logger.Log.Infow("deleting order messages",
		"order_id", orderID,
		"count", len(sentOrders),
	)

	for _, sent := range sentOrders {
		if _, err := h.bot.Request(tgbotapi.NewDeleteMessage(
			sent.ChatID,
			sent.MessageID,
		)); err != nil {
			wrapped := wrapTelegramErr("telegram.delete_message", err)
			logger.Log.Errorw("failed to delete message",
				"chat_id", sent.ChatID,
				"message_id", sent.MessageID,
				"err", wrapped,
			)
		}
	}

	h.orderMessageService.MarkDeletedByOrder(ctx, orderID)
}

func (h *Handler) renderControlPanel(
	ctx context.Context,
	topicID, threadID int64,
	order *domain.Order) {

	isVerified := h.getUserVerified(ctx, order.UserChatID)
	createdTokens := make([]struct {
		token  string
		action string
	}, 0, 3)

	tokenConfirmed, err := h.callbackTokenService.Create(
		ctx,
		"confirmed",
		&ConfirmedAndDeclinedOrderSelectPayload{
			OrderID:  order.ID,
			TopicID:  topicID,
			ThreadID: threadID,
		},
	)
	if err != nil {
		logger.Log.Errorw("failed to create confirmed order callback token",
			"err", err,
		)
	} else {
		createdTokens = append(createdTokens, struct {
			token  string
			action string
		}{
			token:  tokenConfirmed,
			action: "confirmed",
		})
	}

	btnAccept := tgbotapi.NewInlineKeyboardButtonData(
		h.text.AcceptText,
		"confirmed:"+tokenConfirmed,
	)

	tokenDeclined, err := h.callbackTokenService.Create(
		ctx,
		"declined",
		&ConfirmedAndDeclinedOrderSelectPayload{
			OrderID:  order.ID,
			TopicID:  topicID,
			ThreadID: threadID,
		},
	)
	if err != nil {
		logger.Log.Errorw("failed to create declined order callback token",
			"err", err,
		)
	} else {
		createdTokens = append(createdTokens, struct {
			token  string
			action string
		}{
			token:  tokenDeclined,
			action: "declined",
		})
	}

	btnDecline := tgbotapi.NewInlineKeyboardButtonData(
		h.text.DeclineText,
		"declined:"+tokenDeclined,
	)

	acceptRow := []tgbotapi.InlineKeyboardButton{
		btnAccept,
	}

	declineRow := []tgbotapi.InlineKeyboardButton{
		btnDecline,
	}

	keyboardRows := [][]tgbotapi.InlineKeyboardButton{
		acceptRow,
		declineRow,
	}

	if h.verificationEnabled && !isVerified {
		tokenVerification, err := h.callbackTokenService.Create(
			ctx,
			"verification",
			&VerificationSelectPayload{
				OrderID:    order.ID,
				UserChatID: order.UserChatID,
			},
		)
		if err != nil {
			logger.Log.Errorw("failed to create verification callback token",
				"err", err,
			)
		} else {
			createdTokens = append(createdTokens, struct {
				token  string
				action string
			}{
				token:  tokenVerification,
				action: "verification",
			})
		}

		btnVerification := tgbotapi.NewInlineKeyboardButtonData(
			h.text.SendToVerificationText,
			"verification:"+tokenVerification,
		)

		keyboardRows = append(
			keyboardRows,
			[]tgbotapi.InlineKeyboardButton{btnVerification},
		)
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		keyboardRows...,
	)

	msg := tgbotapi.NewMessage(
		topicID,
		h.textDynamic.ApplicationManagementText(
			order.GameNameAtPurchase,
			order.GameTypeNameAtPurchase,
			isVerified,
		),
	)
	msg.MessageThreadID = threadID
	msg.ReplyMarkup = markup

	sent, err := h.bot.Send(msg)
	if err != nil {
		wrapped := wrapTelegramErr("telegram.send_control_panel", err)
		logger.Log.Errorw("failed to send control panel message",
			"order_id", order.ID,
			"topic_id", topicID,
			"err", wrapped,
		)
		for _, item := range createdTokens {
			if err := h.callbackTokenService.Delete(
				ctx,
				item.token,
				item.action,
			); err != nil {
				logger.Log.Errorw("failed to cleanup control panel callback token",
					"order_id", order.ID,
					"token_action", item.action,
					"err", err,
				)
			}
		}
		return
	}

	if err := h.orderMessageService.Save(
		ctx,
		order.ID,
		sent.Chat.ID,
		sent.MessageID,
	); err != nil {
		logger.Log.Errorw("failed to save control panel message",
			"order_id", order.ID,
			"topic_id", topicID,
			"err", err,
		)
	}
}

func (h *Handler) renderEditControlPanel(
	ctx context.Context,
	messageID int,
	topicID, threadID int64,
	order *domain.Order) {

	isVerified := h.getUserVerified(ctx, order.UserChatID)
	createdTokens := make([]struct {
		token  string
		action string
	}, 0, 3)

	tokenConfirmed, err := h.callbackTokenService.Create(
		ctx,
		"confirmed",
		&ConfirmedAndDeclinedOrderSelectPayload{
			OrderID:  order.ID,
			TopicID:  topicID,
			ThreadID: threadID,
		},
	)
	if err != nil {
		logger.Log.Errorw("failed to create confirmed order callback token",
			"err", err,
		)
	} else {
		createdTokens = append(createdTokens, struct {
			token  string
			action string
		}{
			token:  tokenConfirmed,
			action: "confirmed",
		})
	}

	btnAccept := tgbotapi.NewInlineKeyboardButtonData(
		h.text.AcceptText,
		"confirmed:"+tokenConfirmed,
	)

	tokenDeclined, err := h.callbackTokenService.Create(
		ctx,
		"declined",
		&ConfirmedAndDeclinedOrderSelectPayload{
			OrderID:  order.ID,
			TopicID:  topicID,
			ThreadID: threadID,
		},
	)
	if err != nil {
		logger.Log.Errorw("failed to create declined order callback token",
			"err", err,
		)
	} else {
		createdTokens = append(createdTokens, struct {
			token  string
			action string
		}{
			token:  tokenDeclined,
			action: "declined",
		})
	}

	btnDecline := tgbotapi.NewInlineKeyboardButtonData(
		h.text.DeclineText,
		"declined:"+tokenDeclined,
	)

	keyboardRows := [][]tgbotapi.InlineKeyboardButton{
		{btnAccept},
		{btnDecline},
	}

	if h.verificationEnabled && !isVerified {
		tokenVerification, err := h.callbackTokenService.Create(
			ctx,
			"verification",
			&VerificationSelectPayload{
				OrderID:    order.ID,
				UserChatID: order.UserChatID,
			},
		)
		if err != nil {
			logger.Log.Errorw("failed to create verification callback token",
				"err", err,
			)
		} else {
			createdTokens = append(createdTokens, struct {
				token  string
				action string
			}{
				token:  tokenVerification,
				action: "verification",
			})
		}

		btnVerification := tgbotapi.NewInlineKeyboardButtonData(
			h.text.SendToVerificationText,
			"verification:"+tokenVerification,
		)

		keyboardRows = append(keyboardRows, []tgbotapi.InlineKeyboardButton{btnVerification})
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(keyboardRows...)

	editMessage := tgbotapi.NewEditMessageText(
		topicID,
		messageID,
		h.textDynamic.ApplicationManagementText(
			order.GameNameAtPurchase,
			order.GameTypeNameAtPurchase,
			isVerified,
		),
	)
	editMessage.ReplyMarkup = &markup

	if _, err := h.bot.Send(editMessage); err != nil {
		wrapped := wrapTelegramErr("telegram.edit_control_panel", err)
		logger.Log.Errorw("failed to edit control panel message",
			"topic_id", topicID,
			"message_id", messageID,
			"err", wrapped,
		)
		for _, item := range createdTokens {
			if err := h.callbackTokenService.Delete(
				ctx,
				item.token,
				item.action,
			); err != nil {
				logger.Log.Errorw("failed to cleanup edit control panel callback token",
					"order_id", order.ID,
					"token_action", item.action,
					"err", err,
				)
			}
		}
	}
}

func (h *Handler) expertGuard(
	upd tgbotapi.Update,
) bool {

	chatID, ok := extractChatID(upd)
	if !ok {
		return true
	}

	experts, err := h.expertService.GetAllExperts()
	if err != nil {
		return true
	}

	isExpert := false
	for _, e := range experts {
		if chatID == e.TopicID {
			isExpert = true
			break
		}
	}

	if !isExpert {
		return true
	}

	if upd.CallbackQuery != nil {
		return true
	}

	if upd.Message != nil && !upd.Message.IsCommand() {
		return true
	}

	logger.Log.Infow("expert interaction detected",
		"chat_id", chatID,
	)

	if upd.Message != nil && upd.Message.IsCommand() &&
		upd.Message.Command() == "start" {
		return true
	}

	logger.Log.Warnw("expert action blocked",
		"chat_id", chatID,
	)

	return false
}

func (h *Handler) supportGuard(
	upd tgbotapi.Update,
) bool {

	chatID, ok := extractChatID(upd)
	if !ok {
		return true
	}

	support := h.supportService.GetSupport()

	if chatID != support.ChatID {
		return true
	}

	if upd.Message != nil && upd.Message.IsCommand() {
		switch upd.Message.Command() {
		case "start", "search":
			return true
		default:
			logger.Log.Warnw("support action blocked",
				"chat_id", chatID,
			)
			return false
		}
	}

	if upd.Message != nil {
		logger.Log.Warnw("support action blocked",
			"chat_id", chatID,
		)
		return false
	}

	return true
}

func (h *Handler) stateGuard(
	ctx context.Context,
	upd tgbotapi.Update,
) bool {
	if upd.Message != nil {
		if upd.Message.Chat.IsSuperGroup() {
			return true
		}
	}

	if upd.CallbackQuery != nil {
		if upd.CallbackQuery.Message.Chat.IsSuperGroup() {
			return true
		}
	}

	chatID, ok := extractChatID(upd)
	if !ok {
		return true
	}

	state, err := h.stateService.GetStateByChatID(ctx, chatID)
	if err != nil {
		return true
	}
	if state == nil {
		logger.Log.Warnw("user state not found in state guard",
			"chat_id", chatID,
		)
		return true
	}

	if state.State == domain.StateWritingReview {
		if shouldAutoPublishReview(upd) {
			logger.Log.Infow("auto publishing pending review",
				"user_chat_id", state.UserChatID,
				"review_id", state.ReviewID,
			)
			h.publishPendingReview(ctx, state)
		}
	}

	if state.State != domain.StateCommunication {
		return true
	}

	if upd.Message != nil && upd.Message.IsCommand() {
		if _, err := h.bot.Send(tgbotapi.NewMessage(
			chatID,
			h.text.CommunicationBlockedCommandText,
		)); err != nil {
			wrapped := wrapTelegramErr("telegram.send_communication_blocked", err)
			logger.Log.Errorw("failed to send communication blocked message",
				"chat_id", chatID,
				"err", wrapped,
			)
		}

		logger.Log.Warnw("command blocked during communication",
			"user_chat_id", chatID,
			"state", state.State,
		)

		return false
	}

	if upd.Message != nil {
		return true
	}

	if upd.CallbackQuery != nil {
		if strings.HasPrefix(upd.CallbackQuery.Data, "accept_client:") {
			return true
		}

		if strings.HasPrefix(upd.CallbackQuery.Data, "verify:") {
			return true
		}

		h.answerCallback(
			upd.CallbackQuery,
			h.text.CommunicationBlockedCallbackText,
		)

		logger.Log.Warnw("callback blocked during communication",
			"user_chat_id", chatID,
			"data", upd.CallbackQuery.Data,
		)

		return false
	}

	return true
}

func (h *Handler) startGuard(
	ctx context.Context,
	upd tgbotapi.Update,
) bool {
	_ = ctx

	chatID, ok := extractChatID(upd)
	if !ok {
		return true
	}

	experts, err := h.expertService.GetAllExperts()
	if err != nil {
		return true
	}

	isExpert := false
	for _, e := range experts {
		if chatID == e.TopicID {
			isExpert = true
			break
		}
	}

	if isExpert {
		return true
	}

	support := h.supportService.GetSupport()

	if chatID == support.ChatID {
		return true
	}

	if upd.Message != nil && upd.Message.IsCommand() &&
		upd.Message.Command() == "start" {
		return true
	}

	if upd.CallbackQuery != nil {
		return true
	}

	/*
		accepted, _ := h.userPolicyAcceptancesService.IsAccepted(ctx, chatID)
		if !accepted {
			message := tgbotapi.NewMessage(
				chatID,
				h.text.NeedAcceptRulesText,
			)
			message.ParseMode = "Markdown"
			if _, err := h.bot.Send(message); err != nil {
				wrapped := wrapTelegramErr("telegram.send_need_accept_rules", err)
				logger.Log.Errorw("failed to send accept rules message",
					"chat_id", chatID,
					"err", wrapped,
				)
			}

			logger.Log.Warnw("command is blocked, the user did not accept rules",
				"user_chat_id", chatID,
			)

			return false
		}
	*/

	return true
}

func shouldAutoPublishReview(upd tgbotapi.Update) bool {
	if upd.Message != nil {

		if upd.Message.IsCommand() {
			return true
		}

		if upd.Message.Text != "" || upd.Message.Caption != "" {
			return false
		}
	}

	return true
}

func (h *Handler) publishPendingReview(
	ctx context.Context,
	state *domain.UserState,
) {
	if state.ReviewID == nil {
		return
	}

	if err := h.reviewService.Publish(ctx, *state.ReviewID); err != nil {
		logger.Log.Errorw("failed to publish review",
			"review_id", *state.ReviewID,
			"err", err,
		)
		return
	}

	logger.Log.Infow("review published",
		"review_id", *state.ReviewID,
	)

	h.stateService.SetStateIdle(ctx, *state.UserChatID)
}

func extractChatID(upd tgbotapi.Update) (int64, bool) {
	if upd.Message != nil {
		return upd.Message.Chat.ID, true
	}

	if upd.CallbackQuery != nil {
		return upd.CallbackQuery.Message.Chat.ID, true
	}

	return 0, false
}

func (h *Handler) getUserVerified(
	ctx context.Context,
	userChatID int64,
) bool {
	user, err := h.userService.GetByChatID(ctx, userChatID)
	if err != nil || user == nil {
		logger.Log.Warnw("failed to get user verification status",
			"chat_id", userChatID,
			"err", err,
		)
		return false
	}

	return user.IsVerified
}

func (h *Handler) findControlPanelMessageID(
	ctx context.Context,
	orderID int,
	topicID int64,
) (int, bool) {
	messages, err := h.orderMessageService.GetByOrder(ctx, orderID)
	if err != nil {
		logger.Log.Errorw("failed to get order messages for control panel",
			"order_id", orderID,
			"err", err,
		)
		return 0, false
	}

	for _, m := range messages {
		if m.ChatID == topicID {
			return m.MessageID, true
		}
	}

	if len(messages) == 1 {
		return messages[0].MessageID, true
	}

	return 0, false
}

func (h *Handler) answerCallback(
	cb *tgbotapi.CallbackQuery,
	text string,
) {
	if _, err := h.bot.Request(tgbotapi.NewCallback(cb.ID, text)); err != nil {
		wrapped := wrapTelegramErr("telegram.answer_callback", err)
		logger.Log.Errorw("failed to answer callback",
			"callback_id", cb.ID,
			"err", wrapped,
		)
	}
}

func (h *Handler) supportOnly(handler HandlerFunc) HandlerFunc {
	return func(ctx context.Context, msg *tgbotapi.Message) {
		support := h.supportService.GetSupport()
		if msg.Chat.ID != support.ChatID {
			return
		}
		handler(ctx, msg)
	}
}
