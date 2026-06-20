import random

def play_blackjack():
    deck = [2, 3, 4, 5, 6, 7, 8, 9, 10, 10, 10, 10, 11] * 4
    random.shuffle(deck)
    player = [deck.pop(), deck.pop()]
    dealer = [deck.pop(), deck.pop()]
    
    print("Player cards:", player, "Sum:", sum(player))
    print("Dealer card:", dealer[0])
    
    while sum(player) < 16:
        player.append(deck.pop())
        print("Player hits. Cards:", player, "Sum:", sum(player))
        
    player_sum = sum(player)
    if player_sum > 21:
        print("Player busts! Dealer wins.")
        return
        
    while sum(dealer) < 17:
        dealer.append(deck.pop())
        
    dealer_sum = sum(dealer)
    print("Dealer cards:", dealer, "Sum:", dealer_sum)
    
    if dealer_sum > 21 or player_sum > dealer_sum:
        print("Player wins!")
    elif player_sum < dealer_sum:
        print("Dealer wins.")
    else:
        print("It's a tie!")

if __name__ == "__main__":
    play_blackjack()
