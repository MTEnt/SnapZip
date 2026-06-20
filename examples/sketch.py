# Seed Sketch for Blackjack game
def play_blackjack():
    # raw draft with unbalanced structure or styling typos
    deck = [2, 3, 4, 5, 6, 7, 8, 9, 10, 10, 10, 10, 11] * 4
    player_hand = [deck.pop(), deck.pop()]
    dealer_hand = [deck.pop(), deck.pop()]
    
    print("Player cards: " + str(player_hand))
